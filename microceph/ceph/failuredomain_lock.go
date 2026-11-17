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
		logger.Warnf("failure domain transition lock was not held by this transition on release; its lease likely expired and another transition reclaimed it")
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
func serializedFailureDomainTransition(ctx context.Context, s mcTypes.State, fn func() error) error {
	token, err := lockFailureDomainTransitionsFunc(ctx, s)
	if err != nil {
		return err
	}
	defer func() {
		unlockErr := unlockFailureDomainTransitionsFunc(ctx, s, token)
		if unlockErr != nil {
			logger.Warnf("failed to release failure domain transition lock: %v", unlockErr)
		}
	}()
	return fn()
}
