package ceph

import (
	"context"
	"fmt"
	"github.com/canonical/microceph/microceph/common"
	"testing"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

func TestOperations(t *testing.T) {
	suite.Run(t, new(operationsSuite))
}

// operationsSuite is the test suite for maintenance mode.
type operationsSuite struct {
	tests.BaseSuite
	TestStateInterface    *mocks.StateInterface
	TestOSDQueryInterface *mocks.OSDQueryInterface
}

func (s *operationsSuite) TestCheckOsdOkToStopOpsTrue() {
	m := mocks.NewOSDQueryInterface(s.T())
	m.On("List", mock.Anything, mock.Anything).Return(
		types.Disks{
			{
				OSD:      1,
				Location: "microceph-0",
			},
			{
				OSD:      2,
				Location: "microceph-1",
			},
		}, nil).Once()

	// patch OSDQuery
	database.OSDQuery = m

	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "ok-to-stop", "osd.1").Return("ok", nil).Once()

	// patch ProcessExec
	common.ProcessExec = r

	// osd.1 in microceph-0 is okay to stop
	ops := CheckOsdOkToStopOps{ClusterOps: ClusterOps{nil, context.Background()}}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestCheckOsdOkToStopOpsFalse() {
	m := mocks.NewOSDQueryInterface(s.T())
	m.On("List", mock.Anything, mock.Anything).Return(
		types.Disks{
			{
				OSD:      1,
				Location: "microceph-0",
			},
			{
				OSD:      2,
				Location: "microceph-1",
			},
		}, nil).Once()

	// patch OSDQuery
	database.OSDQuery = m

	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "ok-to-stop", "osd.1").Return("fail", fmt.Errorf("some reasons")).Once()

	// patch ProcessExec
	common.ProcessExec = r

	// osd.1 in microceph-0 is not okay to stop
	ops := CheckOsdOkToStopOps{ClusterOps: ClusterOps{nil, context.Background()}}
	err := ops.Run("microceph-0")
	assert.ErrorContains(s.T(), err, "cannot be safely stopped")
}

func (s *operationsSuite) TestCheckOsdOkToStopOpsError() {
	m := mocks.NewOSDQueryInterface(s.T())
	m.On("List", mock.Anything, mock.Anything).Return(types.Disks{}, fmt.Errorf("some reasons")).Once()

	// patch OSDQuery
	database.OSDQuery = m

	// cannot get disks
	ops := CheckOsdOkToStopOps{ClusterOps: ClusterOps{nil, context.Background()}}
	err := ops.Run("some-node-name")
	assert.ErrorContains(s.T(), err, "error listing disks")
}

func (s *operationsSuite) TestSetNooutOpsOkay() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "set", "noout").Return("ok", nil).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := SetNooutOps{}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestSetNooutOpsFail() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "set", "noout").Return("fail", fmt.Errorf("some reasons")).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := SetNooutOps{}
	err := ops.Run("microceph-0")
	assert.Error(s.T(), err)
}

func (s *operationsSuite) TestAssertNooutFlagSetOpsTrue() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump").Return("flags noout", nil).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := AssertNooutFlagSetOps{}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestAssertNooutFlagSetOpsFalse() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump").Return("flags", nil).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := AssertNooutFlagSetOps{}
	err := ops.Run("microceph-0")
	assert.ErrorContains(s.T(), err, "unset")
}

func (s *operationsSuite) TestAssertNooutFlagSetOpsError() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump").Return("fail", fmt.Errorf("some reasons")).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := AssertNooutFlagSetOps{}
	err := ops.Run("microceph-0")
	assert.Error(s.T(), err)
}

func (s *operationsSuite) TestAssertNooutFlagUnsetOpsTrue() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump").Return("flags", nil).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := AssertNooutFlagUnsetOps{}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestAssertNooutFlagUnsetOpsFalse() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump").Return("flags noout", nil).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := AssertNooutFlagUnsetOps{}
	err := ops.Run("microceph-0")
	assert.ErrorContains(s.T(), err, "set")
}

func (s *operationsSuite) TestAssertNooutFlagUnsetOpsError() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump").Return("fail", fmt.Errorf("some reasons")).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := AssertNooutFlagUnsetOps{}
	err := ops.Run("microceph-0")
	assert.Error(s.T(), err)
}

func (s *operationsSuite) TestStopOsdOpsOkay() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "snapctl", "stop", "microceph.osd", "--disable").Return("okay", nil).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := StopOsdOps{ClusterOps: ClusterOps{nil, context.Background()}}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestStopOsdOpsFail() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "snapctl", "stop", "microceph.osd", "--disable").Return("fail", fmt.Errorf("some reasons")).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := StopOsdOps{ClusterOps: ClusterOps{nil, context.Background()}}
	err := ops.Run("microceph-0")
	assert.Error(s.T(), err, "Unable to stop OSD service in node")
}

func (s *operationsSuite) TestStartOsdOpsOkay() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "snapctl", "start", "microceph.osd", "--enable").Return("okay", nil).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := StartOsdOps{ClusterOps: ClusterOps{nil, context.Background()}}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestStartOsdOpsFail() {
	// m := mocks.NewClientInterface(s.T())
	// m.On("SetOsdState", true).Return(fmt.Errorf("some reasons"))
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "snapctl", "start", "microceph.osd", "--enable").Return("fail", fmt.Errorf("some reasons")).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := StartOsdOps{ClusterOps: ClusterOps{nil, context.Background()}}
	err := ops.Run("microceph-0")
	assert.Error(s.T(), err, "Unable to start OSD service in node")
}

func (s *operationsSuite) TestUnSetNooutOpsOkay() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "unset", "noout").Return("ok", nil).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := UnsetNooutOps{}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestUnSetNooutOpsFail() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "unset", "noout").Return("fail", fmt.Errorf("some reasons")).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := UnsetNooutOps{}
	err := ops.Run("microceph-0")
	assert.Error(s.T(), err)
}
