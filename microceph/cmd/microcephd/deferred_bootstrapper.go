package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// DeferredBootstrapper initializes MicroCluster/dqlite only, deferring Ceph
// bootstrap to a later Ceph-only bootstrap operation (CE142). When DeferCeph
// is set, PostBootstrap runs this bootstrapper instead of SimpleBootstrapper,
// so microcephd and dqlite come up immediately while Ceph remains
// not-bootstrapped.
type DeferredBootstrapper struct {
	// AvailabilityZone is the AZ of the bootstrap node, recorded as a host_tag
	// so subsequent deferred joins with --availability-zone are not rejected by
	// validateJoinAZ's mixed-AZ check.
	AvailabilityZone string
}

// Prefill stores the bootstrap config's availability zone for later use.
func (db *DeferredBootstrapper) Prefill(bd common.BootstrapConfig, _ interfaces.StateInterface) error {
	db.AvailabilityZone = bd.AvailabilityZone
	logger.Debug("Deferred bootstrap: prefill recorded AZ")
	return nil
}

// Precheck validates deferred-bootstrap inputs before MicroCluster state is
// modified. Ceph validation is still deferred until the Ceph-only bootstrap
// operation runs.
func (db *DeferredBootstrapper) Precheck(_ context.Context, _ interfaces.StateInterface) error {
	if db.AvailabilityZone != "" && !ceph.IsValidCrushName(db.AvailabilityZone) {
		return fmt.Errorf("invalid availability zone name %q: must match [a-zA-Z0-9_.-]+", db.AvailabilityZone)
	}
	logger.Debug("Deferred bootstrap: precheck successful")
	return nil
}

// Bootstrap records the Ceph lifecycle state as not_bootstrapped, records the
// bootstrap node's availability zone as a host_tag (so deferred joins are not
// rejected by the mixed-AZ check), and returns without creating a Ceph FSID,
// admin keyring, ceph.conf, or MON/MGR/MDS services. MicroCluster membership is
// already initialized by the time this hook runs.
func (db *DeferredBootstrapper) Bootstrap(ctx context.Context, state interfaces.StateInterface) error {
	logger.Info("Deferred bootstrap: initializing MicroCluster only, Ceph bootstrap deferred")

	// Validate again defensively in case Bootstrap is called without Precheck;
	// do this before writing lifecycle state.
	if db.AvailabilityZone != "" && !ceph.IsValidCrushName(db.AvailabilityZone) {
		return fmt.Errorf("invalid availability zone name %q: must match [a-zA-Z0-9_.-]+", db.AvailabilityZone)
	}

	if state.ClusterState().ServerCert() == nil {
		// Without a server cert we cannot write to the database; this path
		// mirrors PopulateBootstrapDatabase's guard. Record state only when
		// the database is reachable.
		logger.Warn("Deferred bootstrap: no server certificate, skipping lifecycle state persistence")
		return nil
	}

	err := setLifecycleStateFunc(ctx, state, database.ClusterLifecycle{
		CephBootstrapState: database.CephStateNotBootstrapped,
	})
	if err != nil {
		return fmt.Errorf("failed to record deferred lifecycle state: %w", err)
	}

	// Record the availability zone as a host_tag so that a subsequent
	// deferred join with --availability-zone is not rejected by the
	// mixed-AZ validation in validateJoinAZ.
	if db.AvailabilityZone != "" {
		err = recordDeferredAZFunc(ctx, state, state.ClusterState().Name(), db.AvailabilityZone)
		if err != nil {
			return fmt.Errorf("failed to record availability zone during deferred bootstrap: %w", err)
		}
	}

	return nil
}

// setLifecycleStateFunc is the injectable function that persists the Ceph
// lifecycle state. It is suffixed with Func per project convention so tests
// can override it.
var setLifecycleStateFunc = func(ctx context.Context, state interfaces.StateInterface, lc database.ClusterLifecycle) error {
	return state.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return database.SetClusterLifecycle(ctx, tx, lc)
	})
}

// recordDeferredAZFunc is the injectable function that records the bootstrap
// node's availability zone as a host_tag. It is suffixed with Func per project
// convention so tests can override it.
var recordDeferredAZFunc = func(ctx context.Context, state interfaces.StateInterface, member string, az string) error {
	return state.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return ceph.RecordHostTagFunc(ctx, tx, member, "availability-zone", az)
	})
}
