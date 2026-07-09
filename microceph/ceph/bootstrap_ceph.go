package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// cephBootstrapMu is a process-local fast-fail guard for Ceph-only bootstrap
// attempts. If another bootstrap is already running in this daemon, callers
// return ErrCephBootstrapInProgress immediately instead of blocking behind a
// detached long-running operation. Cross-member serialization is additionally
// guarded by the lifecycle state in the shared dqlite database.
var cephBootstrapMu sync.Mutex

// ErrCephBootstrapInProgress is returned when a Ceph-only bootstrap is already
// running on the cluster.
var ErrCephBootstrapInProgress = fmt.Errorf("ceph bootstrap already in progress")

// ErrUnknownBootstrapTarget is returned when the requested bootstrap target
// member is not a known MicroCluster member.
var ErrUnknownBootstrapTarget = fmt.Errorf("unknown bootstrap target member")

// ErrPartialBootstrap is returned when a prior Ceph-only bootstrap left
// partial state (fsid/admin keyring config rows present but Ceph not fully
// bootstrapped). Re-running SimpleBootstrapper would generate a divergent FSID
// and conflict with existing DB rows, so the retry is refused for safety until
// the operator cleans up the partial artifacts.
var ErrPartialBootstrap = fmt.Errorf("partial Ceph bootstrap detected")

// BootstrapCephStepsFunc is the injectable function that performs the actual
// Ceph bootstrap steps (FSID, ceph.conf, keyrings, MON/MGR/MDS services). In
// production the daemon layer sets this to a function that reuses
// SimpleBootstrapper logic; in tests it is overridden to avoid subprocess calls.
var BootstrapCephStepsFunc = func(ctx context.Context, s interfaces.StateInterface, target string, bd common.BootstrapConfig) error {
	return fmt.Errorf("ceph bootstrap steps not configured; the daemon must inject an implementation")
}

// verifyExistingCephBootstrapFunc performs a cheap connectivity check before a
// stale lifecycle row is healed from existing Ceph config rows. It is
// injectable so unit tests do not need to run the ceph CLI.
var verifyExistingCephBootstrapFunc = func(ctx context.Context) error {
	_, err := cephRunContext(ctx, "-s")
	if err != nil {
		return fmt.Errorf("failed to verify existing Ceph cluster connectivity: %w", err)
	}
	return nil
}

// GetClusterMemberNamesFunc is the injectable function that returns the names
// of all MicroCluster members. It is used for target validation.
var GetClusterMemberNamesFunc = func(ctx context.Context, s interfaces.StateInterface) ([]string, error) {
	return nil, fmt.Errorf("cluster member listing not configured")
}

// BootstrapCephFunc is the injectable wrapper for BootstrapCeph, used
// by API handlers so tests can override it.
var BootstrapCephFunc = BootstrapCeph

// BootstrapCeph bootstraps Ceph on an existing MicroCluster member (CE142).
// It is targetable by member name, idempotent on retries, and concurrency-safe.
//
//   - If Ceph is already bootstrapped, it succeeds as a no-op (returns nil).
//   - If another bootstrap is in progress, it returns ErrCephBootstrapInProgress.
//   - If force is true and the state is in_progress (a stale bootstrap from a
//     crashed or stuck daemon), the in_progress row is reset to failed so the
//     normal retry can proceed when no existing Ceph config is present. WARNING:
//     force resets ANY in_progress row, including one belonging to a genuinely
//     live bootstrap on another member; it must NOT be invoked while a live
//     bootstrap may be running, or two members can race to bootstrap divergent
//     Ceph clusters. If fsid/admin keyring config rows already exist, the retry
//     first verifies Ceph connectivity and heals a stale lifecycle row on
//     success, or refuses with ErrPartialBootstrap on verification failure.
//   - If the target is not a known MicroCluster member, it returns
//     ErrUnknownBootstrapTarget.
//   - On failure, it records the error detail in the lifecycle state for
//     operator retry.
//
// The transition from not_bootstrapped/failed to in_progress is an atomic
// conditional UPDATE, preventing two members from racing to bootstrap
// divergent Ceph clusters.
//
// The whole operation runs under a context detached from the request's
// cancellation (context.WithoutCancel) so a cancelled client request (e.g.
// CLI timeout or proxy deadline) cannot abort the bootstrap mid-way or strand
// the lifecycle state in in_progress. The request context's values (notably
// the microcluster logger) are preserved.
func BootstrapCeph(ctx context.Context, s interfaces.StateInterface, target string, bd common.BootstrapConfig, force bool) error {
	locked := cephBootstrapMu.TryLock()
	if !locked {
		logger.Debugf("Ceph-only bootstrap: another bootstrap is already running in this daemon")
		return ErrCephBootstrapInProgress
	}
	defer cephBootstrapMu.Unlock()

	if s.ClusterState().ServerCert() == nil {
		return fmt.Errorf("no server certificate")
	}

	// Detach from the request's cancellation while keeping its values
	// (notably the microcluster logger the DB layer reads via
	// log.LoggerFromContext). The Ceph-only bootstrap is a server-side
	// operation that must run to completion and record its result even if the
	// client (CLI 16-min timeout) or the proxy gives up mid-way; aborting it
	// would leave Ceph half-bootstrapped or the lifecycle stranded in
	// in_progress. This 15-minute server-side deadline bounds the whole
	// operation and is intentionally one minute shorter than the CLI's 16-min
	// client-side timeout, so a successful server-side bootstrap is not reported
	// as a client timeout. WithoutCancel keeps the parent's values but is not
	// cancelled when the parent is.
	opCtx, opCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Minute)
	defer opCancel()

	logger.Debugf("Ceph-only bootstrap: starting (target=%s, force=%v)", target, force)

	if force {
		logger.Warnf("Ceph-only bootstrap invoked with --force: this resets an in_progress lifecycle row. Do not use --force while a live bootstrap may be running on another member; doing so can clobber a genuine in-progress bootstrap and lead to divergent cluster state.")
	}

	// Atomically transition lifecycle state to in_progress. This uses a
	// conditional UPDATE that only succeeds when the current state is
	// not_bootstrapped or failed, preventing divergent bootstrap races.
	// When force is true, a stale in_progress row is reset to failed first.
	proceed, err := atomicStartBootstrapFunc(opCtx, s, target, force)
	if err != nil {
		return err
	}
	if !proceed {
		// Already bootstrapped: succeed as a no-op.
		return nil
	}

	// Run the actual Ceph bootstrap steps via the injected function.
	bootErr := BootstrapCephStepsFunc(opCtx, s, target, bd)

	// Update lifecycle state based on the result. Reuse the detached opCtx
	// (carrying the logger, not cancelled with the request) so the result is
	// always recorded even if the client timed out during the steps.
	recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(opCtx), 30*time.Second)
	defer recordCancel()
	recordErr := s.ClusterState().Database().Transaction(recordCtx, func(ctx context.Context, tx *sql.Tx) error {
		if bootErr != nil {
			logger.Errorf("Ceph-only bootstrap failed: %v", bootErr)
			return database.SetClusterLifecycle(ctx, tx, database.ClusterLifecycle{
				CephBootstrapState:  database.CephStateFailed,
				CephBootstrapTarget: target,
				Detail:              bootErr.Error(),
			})
		}
		return database.SetClusterLifecycle(ctx, tx, database.ClusterLifecycle{
			CephBootstrapState:  database.CephStateBootstrapped,
			CephBootstrapTarget: target,
		})
	})
	if recordErr != nil {
		// Fail loudly: the result-recording transaction failed, so the
		// lifecycle row may still be in_progress (stranded). Surface both the
		// recording failure and the original bootstrap error (if any) so the
		// real root cause is not lost. Recovery requires --force.
		logger.Errorf("failed to record ceph bootstrap result (lifecycle may be stranded in in_progress): %v; original bootstrap error: %v", recordErr, bootErr)
		if bootErr != nil {
			return fmt.Errorf("ceph bootstrap failed (%v) and recording the result also failed: %w; lifecycle state may be stale — retry or use --force to recover", bootErr, recordErr)
		}
		return fmt.Errorf("failed to record ceph bootstrap result: %w; lifecycle state may be stale — retry or use --force to recover", recordErr)
	}

	return bootErr
}

// atomicStartBootstrapFunc atomically transitions the lifecycle state from
// not_bootstrapped or failed to in_progress. It returns (true, nil) if the
// transition succeeded (bootstrap should proceed), or (false, nil) if Ceph is
// already bootstrapped (no-op success). It returns an error if another
// bootstrap is in progress or if target validation fails.
//
// When force is true, a stale in_progress row is first reset to failed (via a
// conditional UPDATE) so the subsequent failed->in_progress transition
// succeeds. This is the recovery path for a crashed or stuck bootstrap.
//
// It is injectable for testing via the atomicStartBootstrapFunc var.
var atomicStartBootstrapFunc = atomicStartBootstrap

// atomicStartBootstrap is the real implementation of the atomic state transition.
func atomicStartBootstrap(ctx context.Context, s interfaces.StateInterface, target string, force bool) (bool, error) {
	// Validate the target is a known cluster member BEFORE opening the dqlite
	// transaction: GetClusterMemberNamesFunc makes a network call to the
	// cluster leader, and holding the transaction lock during a network call
	// blocks all other database writers and risks aborting the transaction if
	// the leader changes mid-call.
	members, err := GetClusterMemberNamesFunc(ctx, s)
	if err != nil {
		return false, fmt.Errorf("failed to list cluster members: %w", err)
	}
	if !slices.Contains(members, target) {
		return false, fmt.Errorf("%w: %s", ErrUnknownBootstrapTarget, target)
	}

	recovered, err := recoverStaleBootstrappedLifecycle(ctx, s, target)
	if err != nil {
		return false, err
	}
	if recovered {
		return false, nil
	}

	var proceed bool

	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// If force is requested, reset an in_progress row to failed first so
		// the subsequent failed->in_progress transition can proceed. This is a
		// conditional UPDATE (in_progress -> failed). Note: this resets ANY
		// in_progress row, including a live bootstrap on another member — force
		// is only safe when no live bootstrap is running. See BootstrapCeph
		// doc comment.
		if force {
			res, err := tx.ExecContext(ctx, `
UPDATE cluster_lifecycle
   SET ceph_bootstrap_state = ?, detail = 'force-reset from in_progress'
 WHERE id = 1 AND ceph_bootstrap_state = ?`,
				database.CephStateFailed, database.CephStateInProgress)
			if err != nil {
				return fmt.Errorf("failed to force-reset lifecycle state: %w", err)
			}
			if n, raErr := res.RowsAffected(); raErr == nil && n > 0 {
				logger.Debugf("Ceph-only bootstrap: --force reset %d stale in_progress lifecycle row(s) to failed", n)
			}
		}

		// Defensive: distinguish a fully-bootstrapped cluster from a partial
		// bootstrap using the config rows (fsid + admin keyring) that
		// SimpleBootstrapper writes. recoverStaleBootstrappedLifecycle already
		// handles the normal stale-lifecycle case by verifying Ceph connectivity
		// outside this transaction and marking the lifecycle bootstrapped. Keep
		// this in-transaction guard as a race fallback so a newly-created config
		// row cannot be bootstrapped over.
		lc, err := database.GetClusterLifecycle(ctx, tx)
		if err != nil {
			return fmt.Errorf("failed to read lifecycle state: %w", err)
		}
		alreadyBootstrapped := lc.CephBootstrapState == database.CephStateBootstrapped
		configBootstrapped, err := configIndicatesBootstrapped(ctx, tx)
		if err != nil {
			return fmt.Errorf("failed to check Ceph config for existing bootstrap: %w", err)
		}
		if configBootstrapped {
			if alreadyBootstrapped {
				// Fully bootstrapped: genuine no-op success.
				logger.Debugf("Ceph-only bootstrap: already bootstrapped (lifecycle=%s); no-op", lc.CephBootstrapState)
				proceed = false
				return nil
			}
			// Partial bootstrap: refuse to re-run over it.
			logger.Debugf("Ceph-only bootstrap: partial bootstrap detected (config rows present but lifecycle=%s); refusing", lc.CephBootstrapState)
			return fmt.Errorf("%w: a prior Ceph-only bootstrap left partial state (fsid/admin keyring present but Ceph not fully bootstrapped). Re-running would generate a divergent FSID and conflict with existing DB rows. Clean up the partial bootstrap on %s (remove ceph.conf/keyrings and the fsid/keyring.client.admin config rows) before retrying", ErrPartialBootstrap, target)
		}

		// Atomic conditional UPDATE: only transition from not_bootstrapped or
		// failed to in_progress. This prevents two members from racing.
		result, err := tx.ExecContext(ctx, `
UPDATE cluster_lifecycle
   SET ceph_bootstrap_state = ?, ceph_bootstrap_target = ?, detail = ''
 WHERE id = 1
   AND (ceph_bootstrap_state = ? OR ceph_bootstrap_state = ?)`,
			database.CephStateInProgress, target,
			database.CephStateNotBootstrapped, database.CephStateFailed)
		if err != nil {
			return fmt.Errorf("failed to update lifecycle state: %w", err)
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to check rows affected: %w", err)
		}

		if rowsAffected == 1 {
			// Transition succeeded; bootstrap should proceed.
			logger.Debugf("Ceph-only bootstrap: transitioned lifecycle to in_progress (target=%s)", target)
			proceed = true
			return nil
		}

		// Transition did not succeed: re-read the state to determine why.
		lc, err = database.GetClusterLifecycle(ctx, tx)
		if err != nil {
			return err
		}

		if lc.CephBootstrapState == database.CephStateBootstrapped {
			// Already bootstrapped: no-op success.
			proceed = false
			return nil
		}

		if lc.CephBootstrapState == database.CephStateInProgress {
			logger.Debugf("Ceph-only bootstrap: another bootstrap is in progress (recorded target=%s); refusing", lc.CephBootstrapTarget)
			return ErrCephBootstrapInProgress
		}

		// Unexpected state: return it as an error for diagnosis.
		logger.Debugf("Ceph-only bootstrap: unexpected lifecycle state %q; cannot start", lc.CephBootstrapState)
		return fmt.Errorf("unexpected lifecycle state %q: cannot start bootstrap", lc.CephBootstrapState)
	})

	return proceed, err
}

// recoverStaleBootstrappedLifecycle heals the lifecycle row when bootstrap
// config rows are present and a cheap Ceph connectivity check confirms the
// cluster is usable. This handles the retry case where Ceph bootstrap steps
// succeeded but the final lifecycle-recording transaction failed.
//
// The connectivity check runs locally on the member serving this request (the
// bootstrap target, via ProxyTarget). A member that never rendered a local
// ceph.conf cannot reach the cluster, so a retry that targets a DIFFERENT
// member than the one that did the original (partial) bootstrap can misreport
// a healthy cluster as ErrPartialBootstrap. Retries should therefore reuse the
// original target; the error message names it when it is known.
func recoverStaleBootstrappedLifecycle(ctx context.Context, s interfaces.StateInterface, target string) (bool, error) {
	var staleConfigBootstrap bool
	var recordedTarget string
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		lc, err := database.GetClusterLifecycle(ctx, tx)
		if err != nil {
			return fmt.Errorf("failed to read lifecycle state: %w", err)
		}
		recordedTarget = lc.CephBootstrapTarget
		alreadyBootstrapped := lc.CephBootstrapState == database.CephStateBootstrapped
		if alreadyBootstrapped {
			logger.Debugf("Ceph-only bootstrap: lifecycle already bootstrapped (recorded target=%s); no recovery needed", recordedTarget)
			return nil
		}
		configBootstrapped, err := configIndicatesBootstrapped(ctx, tx)
		if err != nil {
			return fmt.Errorf("failed to check Ceph config for existing bootstrap: %w", err)
		}
		staleConfigBootstrap = configBootstrapped
		return nil
	})
	if err != nil {
		return false, err
	}
	if !staleConfigBootstrap {
		logger.Debugf("Ceph-only bootstrap: no stale config bootstrap detected; skipping recovery")
		return false, nil
	}

	logger.Debugf("Ceph-only bootstrap: stale config bootstrap detected (recordedTarget=%s, thisTarget=%s); verifying Ceph connectivity", recordedTarget, target)

	err = verifyExistingCephBootstrapFunc(ctx)
	if err != nil {
		hint := ""
		if recordedTarget != "" && recordedTarget != target {
			hint = fmt.Sprintf(" Note: the previous bootstrap attempt was recorded on member %q; this verification ran on %q, which may lack a local ceph.conf — retry with --target %s before treating the bootstrap as partial.", recordedTarget, target, recordedTarget)
		}
		return false, fmt.Errorf("%w: fsid/admin keyring config rows exist but Ceph connectivity verification failed: %v. Clean up the partial bootstrap on %s (remove ceph.conf/keyrings and the fsid/keyring.client.admin config rows) before retrying.%s", ErrPartialBootstrap, err, target, hint)
	}

	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return database.SetClusterLifecycle(ctx, tx, database.ClusterLifecycle{
			CephBootstrapState:  database.CephStateBootstrapped,
			CephBootstrapTarget: target,
		})
	})
	if err != nil {
		return false, fmt.Errorf("verified existing Ceph bootstrap but failed to mark lifecycle bootstrapped: %w", err)
	}

	logger.Infof("Recovered stale Ceph lifecycle row for existing bootstrap on %s", target)
	return true, nil
}
