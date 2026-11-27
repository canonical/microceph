package ceph

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/canonical/microceph/microceph/logger"
)

// failureDomainReconcileAttempts bounds the retries of a scheduled
// reconciliation, failureDomainReconcileSleep is the pause between attempts,
// and failureDomainReconcileTimeout bounds the whole scheduled goroutine.
// They are vars so tests can shorten them.
var (
	failureDomainReconcileAttempts = 5
	failureDomainReconcileSleep    = 5 * time.Second
	failureDomainReconcileTimeout  = 15 * time.Minute
)

// failureDomainReconcilePending ensures at most one scheduled reconciliation
// goroutine per daemon: repeated busy transitions coalesce into one retry.
var failureDomainReconcilePending atomic.Bool

// scheduleFailureDomainReconcile records that a failure domain transition was
// skipped because the transition lock was busy, and runs a background
// reconciliation: the retry acquires the lock, re-reads the current topology
// and converges the failure domain without waiting for the next disk add
// (there may be no next disk add — a cluster that just reached its target
// size would otherwise keep a stale failure domain). The pending record is
// in-memory: if the daemon restarts before the retry runs, the next disk
// add re-runs the transition as before.
func (m *OSDManager) scheduleFailureDomainReconcile() {
	if !failureDomainReconcilePending.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer failureDomainReconcilePending.Store(false)
		// The goroutine outlives the request that scheduled it: detached,
		// bounded context.
		ctx, cancel := context.WithTimeout(context.Background(), failureDomainReconcileTimeout)
		defer cancel()
		for attempt := 1; ; attempt++ {
			if attempt > 1 {
				select {
				case <-ctx.Done():
					logger.Warnf("giving up failure domain reconciliation: %v; the next disk add re-runs it", ctx.Err())
					return
				case <-time.After(failureDomainReconcileSleep):
				}
			}
			err := m.reconcileFailureDomain(ctx)
			if err == nil {
				return
			}
			if attempt >= failureDomainReconcileAttempts {
				logger.Warnf("failure domain reconciliation did not converge after %d attempts (last error: %v); the next disk add re-runs it", attempt, err)
				return
			}
			if !errors.Is(err, ErrFailureDomainTransitionBusy) {
				logger.Debugf("failure domain reconciliation attempt %d failed: %v", attempt, err)
			}
		}
	}()
}

// reconcileFailureDomain re-evaluates the cluster's failure domain from the
// current topology under one hold of the transition lock: a rack promotion
// when 3 or more availability zones have OSDs, then the host switch when 3
// or more members have OSDs (a no-op when the rack rule already governs).
// It is the retry body for transitions skipped on a busy lock.
func (m *OSDManager) reconcileFailureDomain(ctx context.Context) error {
	return serializedFailureDomainTransition(ctx, m.state, func() error {
		data, err := getAZData(ctx, m.state, m.state.Name())
		if err != nil {
			return fmt.Errorf("failed to get AZ data: %w", err)
		}
		if len(data.uniqueAZs) >= 3 {
			err = m.promoteToRackFailureDomainLocked(ctx, data)
			if err != nil {
				return err
			}
		}
		return m.updateFailureDomainLocked(ctx, m.state)
	})
}
