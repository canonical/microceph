package ceph

import (
	"context"
	"time"
)

// controlDaemonRemovalVerifyTimeout bounds the post-eviction verification poll
// for MGR/MDS map convergence. It comfortably exceeds the default Ceph beacon
// grace (mon_mgr_beacon_grace, 30s) so that even when the active eviction
// command is a partial no-op for a standby daemon, verification still succeeds
// via beacon aging within the bounded window rather than returning a spurious
// error. It is a var (not a const) so unit tests can shrink it.
var controlDaemonRemovalVerifyTimeout = 60 * time.Second

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
