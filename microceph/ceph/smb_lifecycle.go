package ceph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

var smbStopOrder = []string{"smbd", "winbindd", "ctdbd"}

type smbAppOps struct {
	prepare func(context.Context, string) error
	started func(context.Context, string) error
	start   func(context.Context, string) error
	stop    func(string) error
	ready   func(context.Context, string) error
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
	// WriteFile's mode applies only to creation. An interrupted temporary file
	// must be private before it receives any new identity or diagnostic bytes.
	if err == nil {
		err = os.Chmod(tmp, 0600)
		if err != nil {
			return err
		}
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
	_, err := os.Lstat(filepath.Join(root, "stop-requested"))
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
	// Stop intent precedes draining membership so no late reload can survive retirement.
	smbMembershipMu.Lock()
	defer smbMembershipMu.Unlock()
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
	// Validate dependency order and completeness before any service effects.
	valid := false
	for _, plan := range [][]string{{"smbd"}, {"ctdbd", "smbd"}, {"ctdbd", "winbindd", "smbd"}} {
		valid = valid || reflect.DeepEqual(roles, plan)
	}
	if !valid {
		return fmt.Errorf("unsupported or incomplete SMB service plan")
	}
	err := checkSMBStartIntent(root)
	if err != nil {
		return err
	}
	for _, role := range roles {
		err = ctx.Err()
		if err == nil && ops.prepare != nil {
			err = ops.prepare(ctx, role)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = ops.start(ctx, role)
		}
		if err == nil && ops.started != nil {
			err = ops.started(ctx, role)
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

func smbCoreRoles(spec smbSpec) ([]string, error) {
	seen := map[string]bool{}
	for _, feature := range spec.Features {
		if (feature != "clustered" && feature != "domain") || seen[feature] {
			return nil, fmt.Errorf("unsupported or duplicate SMB feature %q", feature)
		}
		seen[feature] = true
	}
	if seen["domain"] {
		if !seen["clustered"] {
			return nil, fmt.Errorf("AD requires clustered CTDB mode")
		}
		return []string{"ctdbd", "winbindd", "smbd"}, nil
	}
	if seen["clustered"] {
		return []string{"ctdbd", "smbd"}, nil
	}
	return []string{"smbd"}, nil
}

func smbSnapOps(root string) smbAppOps {
	services := newSMBServiceTable(root)
	lookup := func(role string) (smbService, error) {
		service, ok := services[role]
		if !ok {
			return nil, fmt.Errorf("unsupported SMB service %q", role)
		}
		return service, nil
	}
	return smbAppOps{
		start: func(ctx context.Context, role string) error {
			service, err := lookup(role)
			if err != nil {
				return err
			}
			return service.Start(ctx)
		},
		stop: func(role string) error {
			service, err := lookup(role)
			if err != nil {
				return err
			}
			// Compensation must still run after cancellation of placement.
			return service.Stop(context.Background())
		},
		ready: func(ctx context.Context, role string) error {
			service, err := lookup(role)
			if err != nil {
				return err
			}
			return service.Ready(ctx)
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
