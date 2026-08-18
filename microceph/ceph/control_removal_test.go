package ceph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// shrinkDaemonRemovalVerify shrinks the detached verification window so tests
// that exercise the timeout path finish quickly. Originals are restored via
// t.Cleanup.
func shrinkDaemonRemovalVerify(t *testing.T) {
	origTimeout := controlDaemonRemovalVerifyTimeout
	origInterval := controlDaemonRemovalVerifyInterval
	t.Cleanup(func() {
		controlDaemonRemovalVerifyTimeout = origTimeout
		controlDaemonRemovalVerifyInterval = origInterval
	})
	controlDaemonRemovalVerifyTimeout = 30 * time.Millisecond
	controlDaemonRemovalVerifyInterval = 5 * time.Millisecond
}

// --- verifyControlDaemonAbsent ---

func TestVerifyControlDaemonAbsentReturnsOnceAbsent(t *testing.T) {
	shrinkDaemonRemovalVerify(t)
	calls := 0
	presence := func(_ context.Context, _ string) (bool, error) {
		calls++
		if calls < 3 {
			return true, nil
		}
		return false, nil
	}

	absent, err := verifyControlDaemonAbsent(context.Background(), "node-a", presence)
	assert.NoError(t, err)
	assert.True(t, absent)
	assert.Equal(t, 3, calls)
}

func TestVerifyControlDaemonAbsentTimesOutStillPresent(t *testing.T) {
	shrinkDaemonRemovalVerify(t)
	presence := func(_ context.Context, _ string) (bool, error) {
		return true, nil
	}

	absent, err := verifyControlDaemonAbsent(context.Background(), "node-a", presence)
	assert.False(t, absent)
	assert.NoError(t, err)
}

func TestVerifyControlDaemonAbsentReturnsLastError(t *testing.T) {
	shrinkDaemonRemovalVerify(t)
	checkErr := errors.New("map read failed")
	presence := func(_ context.Context, _ string) (bool, error) {
		return false, checkErr
	}

	absent, err := verifyControlDaemonAbsent(context.Background(), "node-a", presence)
	assert.False(t, absent)
	assert.ErrorIs(t, err, checkErr)
}

func TestVerifyControlDaemonAbsentUsesDetachedContext(t *testing.T) {
	shrinkDaemonRemovalVerify(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // parent already cancelled

	sawLiveContext := false
	presence := func(checkCtx context.Context, _ string) (bool, error) {
		// The verification must not use the cancelled parent context.
		if checkCtx.Err() == nil {
			sawLiveContext = true
		}
		return false, nil
	}

	absent, err := verifyControlDaemonAbsent(ctx, "node-a", presence)
	assert.NoError(t, err)
	assert.True(t, absent)
	assert.True(t, sawLiveContext, "verification should run on a fresh, live context")
}

// --- ensureMgrAbsent ---

func mgrMetadata(names ...string) string {
	out := "["
	for i, n := range names {
		if i > 0 {
			out += ","
		}
		out += `{"name":"` + n + `"}`
	}
	return out + "]"
}

func TestEnsureMgrAbsentAlreadyAbsent(t *testing.T) {
	r := withMockRunner(t)
	r.On("RunCommandContext", mock.Anything, "ceph", "mgr", "metadata", "-f", "json").
		Return(mgrMetadata("node-b"), nil).Once()

	err := ensureMgrAbsent(context.Background(), "node-a")
	assert.NoError(t, err)
}

func TestEnsureMgrAbsentEvictsAndConfirms(t *testing.T) {
	shrinkDaemonRemovalVerify(t)
	r := withMockRunner(t)
	// Present before eviction.
	r.On("RunCommandContext", mock.Anything, "ceph", "mgr", "metadata", "-f", "json").
		Return(mgrMetadata("node-a", "node-b"), nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mgr", "fail", "node-a").
		Return("", nil).Once()
	// Absent after eviction (verification poll).
	r.On("RunCommandContext", mock.Anything, "ceph", "mgr", "metadata", "-f", "json").
		Return(mgrMetadata("node-b"), nil).Once()

	err := ensureMgrAbsent(context.Background(), "node-a")
	assert.NoError(t, err)
}

func TestEnsureMgrAbsentAcceptsAmbiguousEvictionWhenVerified(t *testing.T) {
	shrinkDaemonRemovalVerify(t)
	r := withMockRunner(t)
	r.On("RunCommandContext", mock.Anything, "ceph", "mgr", "metadata", "-f", "json").
		Return(mgrMetadata("node-a", "node-b"), nil).Once()
	// `ceph mgr fail` errors (e.g. the daemon was already gone), but the map
	// converges via beacon aging within the verification window.
	r.On("RunCommandContext", mock.Anything, "ceph", "mgr", "fail", "node-a").
		Return("", errors.New("mgr.node-a does not exist")).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mgr", "metadata", "-f", "json").
		Return(mgrMetadata("node-b"), nil).Once()

	err := ensureMgrAbsent(context.Background(), "node-a")
	assert.NoError(t, err)
}

func TestEnsureMgrAbsentPreservesEvictionFailureWhenStillPresent(t *testing.T) {
	shrinkDaemonRemovalVerify(t)
	r := withMockRunner(t)
	evictErr := errors.New("mgr fail rejected")
	r.On("RunCommandContext", mock.Anything, "ceph", "mgr", "metadata", "-f", "json").
		Return(mgrMetadata("node-a", "node-b"), nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mgr", "fail", "node-a").
		Return("", evictErr).Once()
	// Remains present through the verification window.
	r.On("RunCommandContext", mock.Anything, "ceph", "mgr", "metadata", "-f", "json").
		Return(mgrMetadata("node-a", "node-b"), nil)

	err := ensureMgrAbsent(context.Background(), "node-a")
	assert.ErrorIs(t, err, evictErr)
}

// --- ensureMdsAbsent ---

func mdsStandbys(names ...string) string {
	out := `{"fsmap":{"standbys":[`
	for i, n := range names {
		if i > 0 {
			out += ","
		}
		out += `{"name":"` + n + `","state":"up:standby"}`
	}
	out += `],"filesystems":[]}}`
	return out
}

func TestEnsureMdsAbsentAlreadyAbsent(t *testing.T) {
	r := withMockRunner(t)
	r.On("RunCommandContext", mock.Anything, "ceph", "mds", "stat", "-f", "json").
		Return(mdsStandbys("node-b"), nil).Once()

	err := ensureMdsAbsent(context.Background(), "node-a")
	assert.NoError(t, err)
}

func TestEnsureMdsAbsentEvictsAndConfirms(t *testing.T) {
	shrinkDaemonRemovalVerify(t)
	r := withMockRunner(t)
	r.On("RunCommandContext", mock.Anything, "ceph", "mds", "stat", "-f", "json").
		Return(mdsStandbys("node-a", "node-b"), nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mds", "fail", "node-a", "--yes-i-really-mean-it").
		Return("", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mds", "stat", "-f", "json").
		Return(mdsStandbys("node-b"), nil).Once()

	err := ensureMdsAbsent(context.Background(), "node-a")
	assert.NoError(t, err)
}

func TestEnsureMdsAbsentPreservesEvictionFailureWhenStillPresent(t *testing.T) {
	shrinkDaemonRemovalVerify(t)
	r := withMockRunner(t)
	evictErr := errors.New("mds fail rejected")
	r.On("RunCommandContext", mock.Anything, "ceph", "mds", "stat", "-f", "json").
		Return(mdsStandbys("node-a", "node-b"), nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mds", "fail", "node-a", "--yes-i-really-mean-it").
		Return("", evictErr).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "mds", "stat", "-f", "json").
		Return(mdsStandbys("node-a", "node-b"), nil)

	err := ensureMdsAbsent(context.Background(), "node-a")
	assert.ErrorIs(t, err, evictErr)
}
