package ceph

import (
	"context"
	"errors"
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
