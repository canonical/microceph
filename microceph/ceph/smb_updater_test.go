package ceph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

type smbDeadlineLockContext struct {
	context.Context
	calls            int
	observedUnlocked bool
}

func (c *smbDeadlineLockContext) Deadline() (time.Time, bool) {
	c.calls++
	if serviceStartMu.TryLock() {
		c.observedUnlocked = true
		serviceStartMu.Unlock()
	}
	return c.Context.Deadline()
}

type smbUpdaterTestDB struct {
	*mocks.MockDB
	checks int
	timed  bool
}

func (db *smbUpdaterTestDB) IsOpen(ctx context.Context) error {
	db.checks++
	_, db.timed = ctx.Deadline()
	return errors.New("test database not open")
}

func TestSMBUpdaterCreatesWorkDeadlineOnlyInsideLifecycleLock(t *testing.T) {
	ctx := &smbDeadlineLockContext{Context: context.Background()}
	db := &smbUpdaterTestDB{MockDB: &mocks.MockDB{}}
	state := interfaces.CephState{State: &mocks.MockState{DBObj: db}}
	require.NoError(t, runSMBUpdate(ctx, state))
	require.Positive(t, ctx.calls, "observe actual context construction, not source text")
	require.False(t, ctx.observedUnlocked, "creating a timeout before locking consumes it while placement runs")
	require.Equal(t, 1, db.checks)
	require.True(t, db.timed, "database work still has an operation deadline")
}

func TestSMBUpdaterCancelledWhileQueuedDoesNotBeginWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	db := &smbUpdaterTestDB{MockDB: &mocks.MockDB{}}
	state := interfaces.CephState{State: &mocks.MockState{DBObj: db}}
	serviceStartMu.Lock()
	done := make(chan error, 1)
	go func() { done <- runSMBUpdate(ctx, state) }()
	cancel()
	serviceStartMu.Unlock()
	require.ErrorIs(t, <-done, context.Canceled)
	require.Zero(t, db.checks, "shutdown while queued must not begin a new database/refresh operation")
}
