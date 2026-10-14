package ceph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var smbStopOrder = []string{"smbmetrics", "smbd", "smb-remote", "winbindd", "cephfs-proxy", "ctdb-nodes", "ctdbd"}

type smbAppOps struct {
	start func(string) error
	stop  func(string) error
	ready func(context.Context, string) error
}

func smbAtomicWrite(path string, content []byte) error {
	tmp := path + ".tmp"
	err := os.WriteFile(tmp, content, 0600)
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
	for _, role := range roles {
		err = ops.start(role)
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
		if feature != "cephfs-proxy" {
			return nil, fmt.Errorf("SMB native %s preparation is not available in this core milestone", feature)
		}
	}
	roles := []string{}
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
			deadline := time.NewTimer(30 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				err := smbNativeCommand(ctx, "ready", root, role)
				if err == nil {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-deadline.C:
					return fmt.Errorf("%s readiness timed out", role)
				case <-tick.C:
				}
			}
		},
	}
}
