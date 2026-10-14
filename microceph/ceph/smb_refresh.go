package ceph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// SMBLocalStatus reports ownership/readiness without private source fingerprints.
func SMBLocalStatus(ctx context.Context) (types.SMBStatus, error) {
	status := types.SMBStatus{}
	owner, err := os.ReadFile(filepath.Join(smbBase(), "owner.json"))
	if errors.Is(err, os.ErrNotExist) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.ClusterID = string(owner)
	err = validateSMBClusterID(status.ClusterID)
	if err != nil {
		return status, err
	}
	root := smbRoot(status.ClusterID)
	_, err = os.Stat(filepath.Join(root, "stop-requested"))
	status.StopRequested = err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return status, err
	}
	_, err = os.Stat(filepath.Join(root, "diagnostics", "last-error"))
	if err == nil {
		status.Error = "local update failed; configuration is not confirmed applied"
	}
	if !status.StopRequested {
		status.Ready = smbNativeCommand(ctx, "ready", root, "smbd") == nil
	}
	return status, nil
}

// startSMBUpdater has no dependency on the monitor-change loop. It is the only
// native periodic source updater; synchronous enable uses the same apply path.
func startSMBUpdater(ctx context.Context, s interfaces.StateInterface) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			queryCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			if s.ClusterState().Database().IsOpen(queryCtx) == nil {
				serviceStartMu.Lock()
				owner, err := os.ReadFile(filepath.Join(smbBase(), "owner.json"))
				if err == nil && validateSMBClusterID(string(owner)) == nil {
					root := smbRoot(string(owner))
					if checkSMBStartIntent(root) == nil {
						err = enableSMBLocked(queryCtx, s, string(owner), false)
						if err != nil {
							logger.Warn("SMB local update failed; inspect targeted service status")
						}
					}
				}
				serviceStartMu.Unlock()
			}
			cancel()
		}
	}()
}
