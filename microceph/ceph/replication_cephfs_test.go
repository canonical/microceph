package ceph

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/stretchr/testify/assert"
)

// shrinkStabilityRetries lowers the wait loop's attempts/backoff so unit tests
// don't sleep for tens of seconds, and restores them on cleanup.
func shrinkStabilityRetries(t *testing.T, attempts uint, backoff time.Duration) {
	t.Helper()
	origAttempts := cephFSMirrorStabilityAttempts
	origBackoff := cephFSMirrorStabilityBackoff
	cephFSMirrorStabilityAttempts = attempts
	cephFSMirrorStabilityBackoff = backoff
	t.Cleanup(func() {
		cephFSMirrorStabilityAttempts = origAttempts
		cephFSMirrorStabilityBackoff = origBackoff
	})
}

func TestCephFSWaitForMirrorPathsIdle_AllIdle(t *testing.T) {
	shrinkStabilityRetries(t, 3, time.Millisecond)

	paths := []string{"/dir1", "/dir2"}
	fetchers := map[string]peerStatusFetcher{
		"peer-a": func(_ context.Context, _ string) (types.CephFsReplicationMirrorStatusMap, error) {
			return types.CephFsReplicationMirrorStatusMap{
				"/dir1": {State: cephFSMirrorIdleState},
				"/dir2": {State: cephFSMirrorIdleState},
			}, nil
		},
	}

	err := cephFSWaitForMirrorPathsIdle(context.Background(), paths, fetchers)
	assert.NoError(t, err)
}

func TestCephFSWaitForMirrorPathsIdle_PathNeverStabilises(t *testing.T) {
	shrinkStabilityRetries(t, 3, time.Millisecond)

	paths := []string{"/dir1", "/dir2"}
	fetchers := map[string]peerStatusFetcher{
		"peer-a": func(_ context.Context, _ string) (types.CephFsReplicationMirrorStatusMap, error) {
			return types.CephFsReplicationMirrorStatusMap{
				"/dir1": {State: cephFSMirrorIdleState},
				"/dir2": {State: "syncing"},
			}, nil
		},
	}

	err := cephFSWaitForMirrorPathsIdle(context.Background(), paths, fetchers)
	assert.ErrorContains(t, err, "mirror path not stable: /dir2")
}

func TestCephFSWaitForMirrorPathsIdle_PathMissingFromPeer(t *testing.T) {
	shrinkStabilityRetries(t, 2, time.Millisecond)

	paths := []string{"/dir1"}
	fetchers := map[string]peerStatusFetcher{
		"peer-a": func(_ context.Context, _ string) (types.CephFsReplicationMirrorStatusMap, error) {
			return types.CephFsReplicationMirrorStatusMap{}, nil
		},
	}

	err := cephFSWaitForMirrorPathsIdle(context.Background(), paths, fetchers)
	assert.ErrorContains(t, err, "mirror path not stable: /dir1")
}

func TestCephFSWaitForMirrorPathsIdle_StabilisesOnSecondAttempt(t *testing.T) {
	shrinkStabilityRetries(t, 5, time.Millisecond)

	paths := []string{"/dir1"}
	calls := 0
	fetchers := map[string]peerStatusFetcher{
		"peer-a": func(_ context.Context, _ string) (types.CephFsReplicationMirrorStatusMap, error) {
			calls++
			state := "syncing"
			if calls >= 2 {
				state = cephFSMirrorIdleState
			}
			return types.CephFsReplicationMirrorStatusMap{
				"/dir1": {State: state},
			}, nil
		},
	}

	err := cephFSWaitForMirrorPathsIdle(context.Background(), paths, fetchers)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, calls, 2)
}

func TestCephFSWaitForMirrorPathsIdle_FetcherError(t *testing.T) {
	shrinkStabilityRetries(t, 2, time.Millisecond)

	paths := []string{"/dir1"}
	fetchers := map[string]peerStatusFetcher{
		"peer-a": func(_ context.Context, _ string) (types.CephFsReplicationMirrorStatusMap, error) {
			return nil, fmt.Errorf("admin socket gone")
		},
	}

	err := cephFSWaitForMirrorPathsIdle(context.Background(), paths, fetchers)
	assert.ErrorContains(t, err, "mirror path not stable: /dir1")
}

func TestCephFSWaitForMirrorPathsIdle_NoPathsNoFetchers(t *testing.T) {
	assert.NoError(t, cephFSWaitForMirrorPathsIdle(context.Background(), nil, nil))
	assert.NoError(t, cephFSWaitForMirrorPathsIdle(context.Background(), []string{"/x"}, nil))
	assert.NoError(t, cephFSWaitForMirrorPathsIdle(context.Background(), nil, map[string]peerStatusFetcher{
		"peer-a": func(_ context.Context, _ string) (types.CephFsReplicationMirrorStatusMap, error) {
			return nil, nil
		},
	}))
}

func TestCephFSWaitForMirrorPathsIdle_MultiplePeersOneNonIdle(t *testing.T) {
	shrinkStabilityRetries(t, 2, time.Millisecond)

	paths := []string{"/dir1"}
	fetchers := map[string]peerStatusFetcher{
		"peer-a": func(_ context.Context, _ string) (types.CephFsReplicationMirrorStatusMap, error) {
			return types.CephFsReplicationMirrorStatusMap{
				"/dir1": {State: cephFSMirrorIdleState},
			}, nil
		},
		"peer-b": func(_ context.Context, _ string) (types.CephFsReplicationMirrorStatusMap, error) {
			return types.CephFsReplicationMirrorStatusMap{
				"/dir1": {State: "syncing"},
			}, nil
		},
	}

	err := cephFSWaitForMirrorPathsIdle(context.Background(), paths, fetchers)
	assert.ErrorContains(t, err, "mirror path not stable: /dir1")
}
