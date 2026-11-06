package ceph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

// SMBLocalStatus reports ownership/readiness without private source values.
func SMBLocalStatus(ctx context.Context, s interfaces.StateInterface) (types.SMBStatus, error) {
	status := types.SMBStatus{}
	owner, err := os.ReadFile(filepath.Join(smbBase(), "owner.json"))
	if errors.Is(err, os.ErrNotExist) {
		var groups []string
		err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			groups, err = database.GetSMBOwnerGroups(ctx, tx, s.ClusterState().Name())
			return err
		})
		if ctx.Err() != nil {
			return status, ctx.Err()
		}
		if err != nil {
			return status, fmt.Errorf("SMB ownership discovery failed; retained ownership is uncertain")
		}
		if len(groups) > 1 {
			return status, fmt.Errorf("multiple local SMB reservations; retained ownership is uncertain")
		}
		if len(groups) == 1 {
			err = validateSMBClusterID(groups[0])
			if err != nil {
				return status, fmt.Errorf("invalid local SMB reservation; retained ownership is uncertain")
			}
			status.ClusterID = groups[0]
			status.Error = "local owner file is missing; retained ownership is uncertain"
		}
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
	_, err = os.Lstat(filepath.Join(root, "stop-requested"))
	status.StopRequested = err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return status, err
	}
	warning, err := os.ReadFile(filepath.Join(root, "diagnostics", "policy-warning"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return status, err
	}
	status.PolicyWarning = string(warning)
	_, err = os.Stat(filepath.Join(root, "diagnostics", "last-error"))
	if err == nil {
		status.Error = "local application failed; configuration is not confirmed applied"
	} else if !errors.Is(err, os.ErrNotExist) {
		return status, err
	}
	if !status.StopRequested {
		status.Ready = smbReadyNativeFunc(ctx, root, "smbd") == nil
	}
	return status, nil
}
