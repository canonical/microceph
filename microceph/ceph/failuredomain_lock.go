package ceph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/logger"
)

// failureDomainLease bounds how long a failure domain transition may hold the
// cluster-wide dqlite lock before it is considered abandoned (daemon crashed
// mid-transition) and reclaimable by the next writer. A transition is a
// handful of ceph CLI calls, so the lease comfortably exceeds any live one.
const failureDomainLease = 5 * time.Minute

// failureDomainTransitionTimeout bounds how long a transition waits for the
// cluster-wide lock before giving up. It is a var so tests can shorten it.
var failureDomainTransitionTimeout = 2 * time.Minute

// failureDomainLockRetrySleep is the pause between lock acquire attempts. It
// is a var so tests can shorten it.
var failureDomainLockRetrySleep = 500 * time.Millisecond

// ErrFailureDomainTransitionBusy is returned by the lock acquisition when
// another transition holds the cluster-wide lock and the lease-retry budget
// is exhausted.
var ErrFailureDomainTransitionBusy = errors.New("failure domain transition in progress")

// failureDomainUnlockTimeout bounds the lock release. The release runs with a
// detached context: the request context may already be canceled when the
// transition finishes, and a release that fails because of that leaves the
// lock held until lease expiry, wedging transitions cluster-wide.
const failureDomainUnlockTimeout = 30 * time.Second

// lockFailureDomainTransitions acquires the cluster-wide failure domain
// transition lock, waiting up to failureDomainTransitionTimeout for a
// concurrent transition to finish. The returned token must be passed to
// unlockFailureDomainTransitions. A lock older than failureDomainLease is
// treated as abandoned and reclaimed.
func lockFailureDomainTransitions(ctx context.Context, s mcTypes.State) (int64, error) {
	deadline := time.Now().Add(failureDomainTransitionTimeout)
	for {
		token := time.Now().UnixNano()
		staleBefore := token - failureDomainLease.Nanoseconds()

		var acquired bool
		err := s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			acquired, err = database.TryAcquireFailureDomainLock(ctx, tx, token, staleBefore)
			return err
		})
		if err != nil {
			return 0, fmt.Errorf("failed to acquire failure domain transition lock: %w", err)
		}
		if acquired {
			return token, nil
		}

		if time.Now().After(deadline) {
			return 0, fmt.Errorf("%w: lock still held after %s", ErrFailureDomainTransitionBusy, failureDomainTransitionTimeout)
		}
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("%w: cancelled while waiting: %v", ErrFailureDomainTransitionBusy, ctx.Err())
		case <-time.After(failureDomainLockRetrySleep):
		}
	}
}

// unlockFailureDomainTransitions releases the cluster-wide failure domain
// transition lock. Releasing is conditional on the token: if this holder's
// lease expired mid-transition and another writer reclaimed the lock, the
// reclaimer's lock is left alone and a warning is logged.
func unlockFailureDomainTransitions(ctx context.Context, s mcTypes.State, token int64) error {
	var released bool
	err := s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		released, err = database.ReleaseFailureDomainLock(ctx, tx, token)
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to release failure domain transition lock: %w", err)
	}
	if !released {
		logger.Warnf("failure domain transition lock was not held by this transition on release; its lease likely expired mid-transition and another transition reclaimed it: this transition's writes may have interleaved with the reclaimer's — the next disk add or removal re-evaluates and re-converges the failure domain")
	} else {
		logger.Debugf("failure domain transition lock released (token=%d)", token)
	}
	return nil
}

// Injectables for the transition lock, overridable in tests.
var (
	lockFailureDomainTransitionsFunc   = lockFailureDomainTransitions
	unlockFailureDomainTransitionsFunc = unlockFailureDomainTransitions
)

// serializedFailureDomainTransition runs fn while holding the cluster-wide
// failure domain transition lock, making the rule-level decision and the
// rule and pool updates it triggers atomic with respect to transitions on
// other members. The acquire error is returned as-is so callers can
// distinguish a busy lock (ErrFailureDomainTransitionBusy) from a hard
// failure and decide whether to skip or abort.
//
// # Fencing limitations and the residual stale-write window
//
// The lock is time-based: a holder whose lease expired is treated as
// crashed and the next writer reclaims the lock. A holder that is merely
// stalled (a hung ceph call, a suspended VM) rather than dead loses mutual
// exclusion without noticing, and writes it issues after resuming could
// revert a transition the reclaimer completed in the meantime.
//
// True fencing is not available here. Fencing requires the guarded store
// to reject stale writes, and the ceph CLI offers no conditional write for
// the guarded state: `ceph osd pool set <pool> crush_rule` and `ceph
// config set mon osd_pool_default_crush_rule` are both unconditional.
// Re-validating the lock token before each write does not help either:
// the check and the write are still a non-atomic pair with the same
// window, so a stall inside that gap defeats the check.
//
// The effective mitigation lives in switchFailureDomain: it re-reads the
// current default rule and each pool's current rule immediately before
// overwriting them, and skips writes that would revert a transition that
// has already completed. A stale write then additionally requires the
// stall to begin and end inside a single sub-millisecond check-then-write
// gap, not anywhere in the multi-call transition.
//
// What remains is bounded and self-healing:
//   - detection: a transition that lost its lock mid-flight is logged at
//     release time;
//   - re-convergence: every disk add re-runs the rack promotion and the
//     host switch, and every removal re-runs the scale-down, so a
//     superseded failure domain is repaired on the next disk add or
//     removal. The only cost is transient rule churn and one PG remapping
//     cycle, never data loss.
//
// # Hardening option not implemented: ceph call deadlines + reclaim grace
//
// If deployments turn out to expect long machine suspensions (e.g. VM
// freeze/restore), one subcase could be fenced further: a holder stalled
// *inside* a ceph call whose call outlives the lease and lands after the
// reclaimer's takeover. Fencing it would require (a) hard deadlines on
// every ceph call issued inside a transition — a refactor of the crush
// helpers off the context-free RunCommand onto RunCommandContext with a
// bounded context — and (b) a lock reclaimer that, upon acquiring an
// *expired* lease, waits one max-call-duration before its first write so
// the previous holder's in-flight calls have all timed out. Note this
// still does not fence a holder suspended *between* the guard read and
// the write (no call is in flight at all then); only a store-side
// conditional write would close the window fully, so the residual gap is
// accepted and documented rather than engineered around.
func serializedFailureDomainTransition(ctx context.Context, s mcTypes.State, fn func() error) error {
	token, err := lockFailureDomainTransitionsFunc(ctx, s)
	if err != nil {
		return err
	}
	defer func() {
		// Release with a fresh detached deadline: the request context may be
		// canceled by the time the transition finishes (client disconnect,
		// request timeout), and a release that fails because of that leaves
		// the lock held until lease expiry. Same pattern as the placement
		// apply lock release.
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failureDomainUnlockTimeout)
		defer cancel()
		unlockErr := unlockFailureDomainTransitionsFunc(unlockCtx, s, token)
		if unlockErr != nil {
			logger.Warnf("failed to release failure domain transition lock (another transition can reclaim it once the lease expires): %v", unlockErr)
		}
	}()
	return fn()
}
