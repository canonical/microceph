package ceph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// MarkCephBootstrappedFunc records that Ceph has been bootstrapped in the
// cluster_lifecycle table. The non-deferred bootstrappers
// (SimpleBootstrapper, AdoptBootstrapper) call this on successful bootstrap so
// the lifecycle state reflects reality for clusters created after the
// cluster_lifecycle schema was introduced (CE142). Without this, a fresh
// non-deferred cluster would be left not_bootstrapped even though Ceph is up,
// breaking ApplyPlacement's pre-bootstrap guard and BootstrapCeph's
// idempotence.
//
// It no-ops when no server certificate is available, mirroring
// PopulateBootstrapDatabase's guard, so it is safe to call from test
// bootstrappers that have no database. It is injectable for testing.
var MarkCephBootstrappedFunc = func(ctx context.Context, s interfaces.StateInterface) error {
	if s.ClusterState().ServerCert() == nil {
		return nil
	}
	return s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return database.SetClusterLifecycle(ctx, tx, database.ClusterLifecycle{
			CephBootstrapState: database.CephStateBootstrapped,
		})
	})
}

// configIndicatesBootstrapped reports whether the config table holds an fsid and
// an admin keyring, which SimpleBootstrapper.Bootstrap writes via
// PopulateBootstrapDatabase. This is the defensive signal used when the
// cluster_lifecycle row is stale (e.g. a crash between PopulateBootstrapDatabase
// and MarkCephBootstrappedFunc, or an upgrade edge case) so that placement is
// not rejected and a second bootstrap is not attempted over an existing cluster.
//
// It uses raw SQL (not the generated mapper) so it works against any
// transaction, including the in-memory SQLite used in unit tests, and avoids the
// microcluster statement-registry dependency.
func configIndicatesBootstrapped(ctx context.Context, tx *sql.Tx) (bool, error) {
	var fsid string
	err := tx.QueryRowContext(ctx, `SELECT value FROM config WHERE key = 'fsid'`).Scan(&fsid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read fsid config: %w", err)
	}
	var adminKey string
	err = tx.QueryRowContext(ctx, `SELECT value FROM config WHERE key = ?`, constants.AdminKeyringFieldName).Scan(&adminKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read admin keyring config: %w", err)
	}
	return fsid != "" && adminKey != "", nil
}

// cephBootstrappedFromConfigFunc is the standalone (own-transaction) wrapper
// around configIndicatesBootstrapped, for callers not already inside a
// transaction (e.g. the ApplyPlacement pre-bootstrap guard). Injectable.
var cephBootstrappedFromConfigFunc = func(ctx context.Context, s interfaces.StateInterface) (bool, error) {
	var bootstrapped bool
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		bootstrapped, err = configIndicatesBootstrapped(ctx, tx)
		return err
	})
	return bootstrapped, err
}

// cephIsBootstrappedFunc reports whether Ceph has been bootstrapped. The
// cluster_lifecycle row is the primary signal; the config-table presence of
// fsid + admin keyring is a defensive fallback so a stale lifecycle row cannot
// cause placement to be rejected or a second bootstrap to be attempted over an
// existing cluster. Injectable for testing.
var cephIsBootstrappedFunc = func(ctx context.Context, s interfaces.StateInterface) (bool, error) {
	lc, err := getClusterLifecycleFunc(ctx, s)
	if err == nil && lc.CephBootstrapState == database.CephStateBootstrapped {
		return true, nil
	}
	if err != nil {
		logger.Warnf("failed to read lifecycle state for bootstrapped check: %v", err)
	}
	return cephBootstrappedFromConfigFunc(ctx, s)
}
