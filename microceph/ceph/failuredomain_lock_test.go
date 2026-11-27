package ceph

import (
	"context"
	"testing"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/stretchr/testify/assert"
)

// TestSerializedTransitionUnlockSurvivesRequestCancellation is the regression
// test for the deferred unlock in serializedFailureDomainTransition: the
// request context may be canceled while the transition runs (client
// disconnect, request timeout), and a release attempted with a canceled
// context fails in database/sql — leaving the cluster-wide lock held until
// lease expiry. The unlock must run with a detached, bounded context instead.
func TestSerializedTransitionUnlockSurvivesRequestCancellation(t *testing.T) {
	origLock := lockFailureDomainTransitionsFunc
	origUnlock := unlockFailureDomainTransitionsFunc
	t.Cleanup(func() {
		lockFailureDomainTransitionsFunc = origLock
		unlockFailureDomainTransitionsFunc = origUnlock
	})

	lockFailureDomainTransitionsFunc = func(_ context.Context, _ mcTypes.State) (int64, error) {
		return 42, nil
	}
	var unlockCtxErr error
	var unlockHasDeadline bool
	var unlockToken int64
	unlockFailureDomainTransitionsFunc = func(ctx context.Context, _ mcTypes.State, token int64) error {
		// Capture the context state at call time: the release's own cleanup
		// cancels it again before serializedFailureDomainTransition returns.
		unlockCtxErr = ctx.Err()
		_, unlockHasDeadline = ctx.Deadline()
		unlockToken = token
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	fn := func() error {
		// The request is canceled while the transition runs.
		cancel()
		return nil
	}
	err := serializedFailureDomainTransition(ctx, nil, fn)
	assert.NoError(t, err)

	assert.Equal(t, int64(42), unlockToken, "unlock must be called with the acquired token")
	assert.NoError(t, unlockCtxErr,
		"unlock must not run with the canceled request context: a failed release wedges the lock until lease expiry")
	assert.True(t, unlockHasDeadline, "unlock context must be bounded so a wedged database cannot hang the release forever")
}
