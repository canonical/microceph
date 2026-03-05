package ceph

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

type monitorSuite struct {
	tests.BaseSuite
}

func TestMonitor(t *testing.T) {
	suite.Run(t, new(monitorSuite))
}

func (s *monitorSuite) SetupTest() {
	s.BaseSuite.SetupTest()
}

func (s *monitorSuite) TestWaitForCephReadyRetriesThenSucceeds() {
	r := mocks.NewRunner(s.T())

	// Fail twice, then succeed.
	r.On("RunCommandContext", mock.Anything, "ceph", "-s").Return("", errors.New("connection refused")).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "-s").Return("", errors.New("connection refused")).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "-s").Return("HEALTH_OK", nil).Once()

	common.ProcessExec = r

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := WaitForCephReady(ctx)
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

func (s *monitorSuite) TestWaitForCephReadyTimeout() {
	r := mocks.NewRunner(s.T())

	// Always fail.
	r.On("RunCommandContext", mock.Anything, "ceph", "-s").Return("", errors.New("connection refused"))

	common.ProcessExec = r

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := WaitForCephReady(ctx)
	assert.Error(s.T(), err)
	assert.ErrorIs(s.T(), err, context.DeadlineExceeded)
}

func (s *monitorSuite) TestWaitForCephReadyImmediateSuccess() {
	r := mocks.NewRunner(s.T())

	r.On("RunCommandContext", mock.Anything, "ceph", "-s").Return("HEALTH_WARN", nil).Once()

	common.ProcessExec = r

	err := WaitForCephReady(context.Background())
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

// osdDumpJSON generates a JSON OSD dump with the given number of up OSDs.
func osdDumpJSON(upCount int) string {
	return osdDumpJSONMixed(upCount, 0)
}

// osdDumpJSONMixed generates a JSON OSD dump with a mix of up and down OSDs.
func osdDumpJSONMixed(upCount, downCount int) string {
	osds := "["
	for i := 0; i < upCount; i++ {
		if i > 0 {
			osds += ","
		}
		osds += fmt.Sprintf(`{"uuid":"osd-%d","up":1}`, i)
	}
	for i := 0; i < downCount; i++ {
		if upCount > 0 || i > 0 {
			osds += ","
		}
		osds += fmt.Sprintf(`{"uuid":"osd-down-%d","up":0}`, i)
	}
	osds += "]"
	return fmt.Sprintf(`{"osds":%s}`, osds)
}

func (s *monitorSuite) TestWaitForOSDsReadyImmediateSuccess() {
	r := mocks.NewRunner(s.T())

	// 1 pool with size=1.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "ls", "--format", "json").
		Return(`["mypool"]`, nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "get", "mypool", "all", "--format", "json").
		Return(`{"pool":"mypool","pool_id":1,"size":1,"min_size":1,"crush_rule":""}`, nil).Once()
	// Found 1 OSD.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "dump", "-f", "json-pretty").
		Return(osdDumpJSON(1), nil).Once()

	common.ProcessExec = r

	// Should succeed: required 1 replica, found 1 OSD.
	err := WaitForOSDsReady(context.Background())
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

func (s *monitorSuite) TestWaitForOSDsReadyRetriesThenSucceeds() {
	r := mocks.NewRunner(s.T())

	// Pool with size=3 (called each iteration).
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "ls", "--format", "json").
		Return(`["rbd"]`, nil)
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "get", "rbd", "all", "--format", "json").
		Return(`{"pool":"rbd","pool_id":1,"size":3,"min_size":2,"crush_rule":""}`, nil)
	// First poll: only 2 OSDs up.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "dump", "-f", "json-pretty").
		Return(osdDumpJSON(2), nil).Once()
	// Second poll: 3 OSDs up.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "dump", "-f", "json-pretty").
		Return(osdDumpJSON(3), nil).Once()

	common.ProcessExec = r

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := WaitForOSDsReady(ctx)
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

func (s *monitorSuite) TestWaitForOSDsReadyWithDownOSDs() {
	r := mocks.NewRunner(s.T())

	// Pool with size=3 (called each iteration).
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "ls", "--format", "json").
		Return(`["rbd"]`, nil)
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "get", "rbd", "all", "--format", "json").
		Return(`{"pool":"rbd","pool_id":1,"size":3,"min_size":2,"crush_rule":""}`, nil)
	// First poll: 2 up, 2 down — not enough.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "dump", "-f", "json-pretty").
		Return(osdDumpJSONMixed(2, 2), nil).Once()
	// Second poll: 3 up, 1 down — sufficient.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "dump", "-f", "json-pretty").
		Return(osdDumpJSONMixed(3, 1), nil).Once()

	common.ProcessExec = r

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := WaitForOSDsReady(ctx)
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

func (s *monitorSuite) TestWaitForOSDsReadyFallbackToDefault() {
	r := mocks.NewRunner(s.T())

	// No pools.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "ls", "--format", "json").
		Return(`[]`, nil).Once()
	// Default size = 3.
	r.On("RunCommandContext", mock.Anything, "ceph", "config", "get", "mon", "osd_pool_default_size").
		Return("3\n", nil).Once()
	// 3 OSDs up.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "dump", "-f", "json-pretty").
		Return(osdDumpJSON(3), nil).Once()

	common.ProcessExec = r

	err := WaitForOSDsReady(context.Background())
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

func (s *monitorSuite) TestWaitForOSDsReadyTimeout() {
	r := mocks.NewRunner(s.T())

	// 1 pool with size=1 (called each iteration).
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "ls", "--format", "json").
		Return(`["mypool"]`, nil)
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "get", "mypool", "all", "--format", "json").
		Return(`{"pool":"mypool","pool_id":1,"size":1,"min_size":1,"crush_rule":""}`, nil)
	// OSD dump always fails.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "dump", "-f", "json-pretty").
		Return("", errors.New("connection refused"))

	common.ProcessExec = r

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := WaitForOSDsReady(ctx)
	assert.Error(s.T(), err)
	assert.ErrorIs(s.T(), err, context.DeadlineExceeded)
}
