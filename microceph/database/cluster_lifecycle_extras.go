package database

import (
	"context"
	"database/sql"
	"fmt"
)

// Ceph lifecycle states for role-managed deployments (CE142).
const (
	// CephStateNotBootstrapped means MicroCluster is initialized but Ceph has not been bootstrapped.
	CephStateNotBootstrapped = "not_bootstrapped"
	// CephStateInProgress means a Ceph-only bootstrap operation is running.
	CephStateInProgress = "in_progress"
	// CephStateBootstrapped means Ceph has been successfully bootstrapped.
	CephStateBootstrapped = "bootstrapped"
	// CephStateFailed means a Ceph-only bootstrap attempt failed or partially completed.
	CephStateFailed = "failed"
)

// ClusterLifecycle is the single-row record tracking Ceph bootstrap lifecycle state.
type ClusterLifecycle struct {
	CephBootstrapped    bool
	CephBootstrapState  string
	CephBootstrapTarget string
	Detail              string
}

// GetClusterLifecycle reads the single-row cluster lifecycle record.
func GetClusterLifecycle(ctx context.Context, tx *sql.Tx) (*ClusterLifecycle, error) {
	var lc ClusterLifecycle
	var bootstrapped int64
	var state, target, detail string
	state = CephStateNotBootstrapped

	row := tx.QueryRowContext(ctx, `
SELECT ceph_bootstrapped, ceph_bootstrap_state, coalesce(ceph_bootstrap_target, ''), coalesce(detail, '')
  FROM cluster_lifecycle WHERE id = 1`)
	err := row.Scan(&bootstrapped, &state, &target, &detail)
	if err != nil {
		return nil, fmt.Errorf("failed to read cluster lifecycle: %w", err)
	}

	lc.CephBootstrapped = bootstrapped != 0
	lc.CephBootstrapState = state
	lc.CephBootstrapTarget = target
	lc.Detail = detail
	return &lc, nil
}

// SetClusterLifecycle upserts the single-row cluster lifecycle record.
func SetClusterLifecycle(ctx context.Context, tx *sql.Tx, lc ClusterLifecycle) error {
	bootstrapped := 0
	if lc.CephBootstrapped {
		bootstrapped = 1
	}
	result, err := tx.ExecContext(ctx, `
UPDATE cluster_lifecycle
   SET ceph_bootstrapped = ?, ceph_bootstrap_state = ?, ceph_bootstrap_target = ?, detail = ?
 WHERE id = 1`, bootstrapped, lc.CephBootstrapState, lc.CephBootstrapTarget, lc.Detail)
	if err != nil {
		return fmt.Errorf("failed to update cluster lifecycle: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("cluster_lifecycle singleton row not found")
	}
	return nil
}
