package ceph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/canonical/microceph/microceph/common"
)

var smbStopOrder = []string{"smbmetrics", "smbd", "smb-remote", "winbindd", "cephfs-proxy", "ctdb-nodes", "ctdbd"}

type smbAppOps struct {
	start func(string) error
	stop  func(string) error
	ready func(context.Context, string) error
}

func smbAtomicWrite(path string, content []byte) error {
	err := smbPrivateDir(filepath.Dir(path))
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	info, err := os.Lstat(tmp)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("SMB temporary file is a symlink")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	err = os.WriteFile(tmp, content, 0600)
	if err != nil {
		return err
	}
	err = os.Rename(tmp, path)
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func checkSMBStartIntent(root string) error {
	_, err := os.Stat(filepath.Join(root, "stop-requested"))
	if err == nil {
		return fmt.Errorf("SMB stop requested; explicit enable required after confirmed stop")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func stopSMBApps(root string, ops smbAppOps) error {
	err := smbAtomicWrite(filepath.Join(root, "stop-requested"), []byte("stop requested\n"))
	if err != nil {
		return err
	}
	failures := []error{}
	for _, role := range smbStopOrder {
		err = ops.stop(role)
		if err != nil {
			failures = append(failures, fmt.Errorf("stop %s: %w", role, err))
		}
	}
	return errors.Join(failures...)
}

func startSMBApps(ctx context.Context, root string, roles []string, ops smbAppOps) error {
	err := checkSMBStartIntent(root)
	if err != nil {
		return err
	}
	for i, role := range roles {
		err = ctx.Err()
		if err == nil {
			err = ops.start(role)
		}
		if err == nil && role == "ctdbd" {
			// CTDB controls run in the confined node monitor's private mount
			// context. Start that fixed peer before waiting for either role.
			if i+1 < len(roles) && roles[i+1] == "ctdb-nodes" {
				continue
			}
			err = fmt.Errorf("CTDB readiness requires the node monitor")
		}
		if err == nil && role == "ctdb-nodes" {
			err = ops.ready(ctx, "ctdbd")
		}
		if err == nil {
			err = ops.ready(ctx, role)
		}
		if err != nil {
			// Attempted roles may have started despite a failed control response.
			// Persist intent and stop every fixed app; preserve ownership on uncertainty.
			stopErr := stopSMBApps(root, ops)
			if stopErr == nil {
				stopErr = os.Remove(filepath.Join(root, "stop-requested"))
			}
			return errors.Join(fmt.Errorf("SMB %s startup/readiness failed: %w", role, err), stopErr)
		}
	}
	return nil
}

func smbCoreRoles(spec smbSpec, proxy bool) ([]string, error) {
	// Optional feature stages attach their preparation/readiness here. Do not
	// start a partially configured domain/cluster/remote service as successful.
	for _, feature := range spec.Features {
		if feature != "cephfs-proxy" && feature != "clustered" {
			return nil, fmt.Errorf("SMB native %s preparation is not available in this core milestone", feature)
		}
	}
	roles := []string{}
	if spec.hasFeature("clustered") {
		roles = append(roles, "ctdbd", "ctdb-nodes")
	}
	if proxy {
		roles = append(roles, "cephfs-proxy")
	}
	return append(roles, "smbd", "smbmetrics"), nil
}

func smbSnapOps(root string) smbAppOps {
	return smbAppOps{
		start: func(role string) error { return snapStart(role, true) },
		stop: func(role string) error {
			err := snapStop(role, true)
			if err != nil {
				return err
			}
			// snapd stop is synchronous; separately confirm there is no active app.
			return smbWaitInactive(role)
		},
		ready: func(ctx context.Context, role string) error {
			budget := 30 * time.Second
			if role == "ctdbd" {
				// A new/rejoining owner can miss the peer updater's tick. Allow
				// one tick + bounded publication before the usual convergence
				// window (pinned CTDB also waits RerecoveryTimeout+3 after
				// recovery). The monitor still requires PNN/NORMAL/full map
				// health; this is not permission for eventual startup success.
				// The caller's deadline, including the 120s client limit, wins.
				budget += smbUpdateInterval + smbUpdateTimeout
			}
			waitCtx, cancel := context.WithTimeout(ctx, budget)
			defer cancel()
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				err := waitCtx.Err()
				if err == nil {
					err = smbNativeCommand(waitCtx, "ready", root, role)
				}
				if err == nil {
					// A helper response racing cancellation is not fresh health.
					return waitCtx.Err()
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-waitCtx.Done():
					if ctx.Err() != nil {
						return ctx.Err()
					}
					return fmt.Errorf("%s readiness timed out", role)
				case <-tick.C:
				}
			}
		},
	}
}

// smbPrivateDir refuses symlink traversal before writing native private state.
func smbPrivateDir(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	parent := filepath.Dir(path)
	if parent != path {
		err = smbPrivateDir(parent)
		if err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.Mkdir(path, 0700)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("SMB private path is not a real directory")
	}
	return nil
}

// refreshSMBApps preserves snapd enable/disable intent and never controls CTDB.
// Restart rather than reload smbd to renew its NSS binds and import changed SAM.
// Readiness is observed separately after recording completed configuration work.
func refreshSMBApps(root string, roles []string) error {
	for _, role := range roles {
		if role == "ctdbd" || role == "ctdb-nodes" {
			continue
		}
		err := checkSMBStartIntent(root)
		if err != nil {
			return err
		}
		// Do not resurrect a separately disabled app on an update.
		out, err := common.ProcessExec.RunCommand("snapctl", "services", "microceph."+role)
		if err != nil {
			return fmt.Errorf("SMB service intent check failed")
		}
		enabled := false
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[0] == "microceph."+role {
				enabled = fields[1] == "enabled"
			}
		}
		if !enabled {
			return fmt.Errorf("SMB %s is disabled or its intent is unknown", role)
		}
		err = snapRestart(role, false)
		if err != nil {
			return fmt.Errorf("SMB %s configuration restart failed", role)
		}
	}
	return nil
}
