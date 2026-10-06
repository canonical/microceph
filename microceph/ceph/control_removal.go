package ceph

import (
	"context"
	"time"
)

// controlDaemonRemovalVerifyTimeout bounds the post-eviction verification poll
// for MGR/MDS map convergence. It must comfortably exceed the worst-case
// beacon-aging fallback so that even when the active eviction command is a
// partial no-op (a standby daemon) or is rejected, verification still succeeds
// via beacon aging within the bounded window rather than returning a spurious
// error.
//
// The window is sized with generous margin over that fallback, not merely equal
// to it: the default grace periods are mon_mgr_beacon_grace (30s) and
// mds_beacon_grace (15s), but MDS teardown runs right after a MON is removed,
// and a MON quorum/leader change resets the monitor's beacon last-seen timers,
// so the effective aging window can restart from the new election. A timeout
// equal to a single grace period could therefore expire just before Ceph ages a
// stopped daemon out, skipping cleanup. 120s leaves clear headroom above the
// stacked worst case. It is a var (not a const) so unit tests can shrink it.
var controlDaemonRemovalVerifyTimeout = 120 * time.Second

// controlDaemonRemovalVerifyInterval is the poll cadence for the verification
// loop above. It is a var so unit tests can shrink it.
var controlDaemonRemovalVerifyInterval = 2 * time.Second

// verifyControlDaemonAbsent polls presenceFunc until hostname is absent from the
// relevant Ceph map or the bounded timeout elapses.
//
// Like the MON removal verification, it runs against a fresh, detached context
// so an ambiguous eviction outcome stays resumable even when the original
// request was cancelled while the eviction command's response was in flight.
// The poll (rather than a single check) absorbs both map propagation lag and
// the slower beacon-aging fallback when the eviction command did not itself
// remove the daemon from the map.
//
// Returns (true, nil) once the daemon is absent. On timeout it returns
// (false, lastErr): lastErr is the most recent presence-check error, or nil
// when the daemon simply remained present without error.
func verifyControlDaemonAbsent(ctx context.Context, hostname string, presenceFunc func(context.Context, string) (bool, error)) (bool, error) {
	verifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), controlDaemonRemovalVerifyTimeout)
	defer cancel()

	var lastErr error
	for {
		present, err := presenceFunc(verifyCtx, hostname)
		if err == nil && !present {
			return true, nil
		}
		lastErr = err

		select {
		case <-verifyCtx.Done():
			return false, lastErr
		case <-time.After(controlDaemonRemovalVerifyInterval):
		}
	}
}
