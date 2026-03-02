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
	r.On("RunCommand", "ceph", "osd", "dump", "-f", "json").Return(`{"flags_set":["sortbitwise","noout"]}`, nil).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := AssertNooutFlagSetOps{}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestAssertNooutFlagSetOpsFalse() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump", "-f", "json").Return(`{"flags_set":["sortbitwise"]}`, nil).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := AssertNooutFlagSetOps{}
	err := ops.Run("microceph-0")
	assert.ErrorContains(s.T(), err, "unset")
}

func (s *operationsSuite) TestAssertNooutFlagSetOpsError() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump", "-f", "json").Return("fail", fmt.Errorf("some reasons")).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := AssertNooutFlagSetOps{}
	err := ops.Run("microceph-0")
	assert.Error(s.T(), err)
}

func (s *operationsSuite) TestAssertNooutFlagUnsetOpsTrue() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump", "-f", "json").Return(`{"flags_set":["sortbitwise"]}`, nil).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := AssertNooutFlagUnsetOps{}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestAssertNooutFlagUnsetOpsFalse() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump", "-f", "json").Return(`{"flags_set":["sortbitwise","noout"]}`, nil).Once()

	// patch ProcessExec
	common.ProcessExec = r

	ops := AssertNooutFlagUnsetOps{}
	err := ops.Run("microceph-0")
	assert.ErrorContains(s.T(), err, "set")
}

func (s *operationsSuite) TestAssertNooutFlagUnsetOpsError() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump", "-f", "json").Return("fail", fmt.Errorf("some reasons")).Once()

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

// TestCheckNonOsdSvcEnoughOpsPass tests the happy path: 3 MONs registered,
// all 3 active, target is n0. After removing n0, 2 MONs remain (>= majority 2). Pass.
func (s *operationsSuite) TestCheckNonOsdSvcEnoughOpsPass() {
	// Mock ServiceQuery: 3 MON services on n0, n1, n2
	sq := mocks.NewServiceQueryInterface(s.T())
	sq.On("List", mock.Anything, mock.Anything).Return(
		types.Services{
			{Service: "mon", Location: "n0"},
			{Service: "mon", Location: "n1"},
			{Service: "mon", Location: "n2"},
		}, nil).Once()
	database.ServiceQuery = sq

	// Mock ProcessExec for getActiveMons, getActiveMgrs, getActiveMdss
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "-s", "-f", "json").Return(
		`{"quorum_names":["n0","n1","n2"]}`, nil).Once()
	r.On("RunCommand", "ceph", "mgr", "dump", "-f", "json").Return(
		`{"active_name":"n0","standbys":[{"name":"n1"}]}`, nil).Once()
	r.On("RunCommand", "ceph", "fs", "status", "-f", "json").Return(
		`{"mdsmap":[{"name":"n0"},{"name":"n1"}]}`, nil).Once()
	common.ProcessExec = r

	ops := CheckNonOsdSvcEnoughOps{ClusterOps: ClusterOps{nil, context.Background()}}
	err := ops.Run("n0")
	assert.NoError(s.T(), err)
}

// TestCheckNonOsdSvcEnoughOpsFailMonQuorum tests MON quorum failure: 3 MONs registered,
// only 2 active (n0, n1), target is n0. After removing n0, 1 MON remains (< majority 2). Fail.
func (s *operationsSuite) TestCheckNonOsdSvcEnoughOpsFailMonQuorum() {
	sq := mocks.NewServiceQueryInterface(s.T())
	sq.On("List", mock.Anything, mock.Anything).Return(
		types.Services{
			{Service: "mon", Location: "n0"},
			{Service: "mon", Location: "n1"},
			{Service: "mon", Location: "n2"},
		}, nil).Once()
	database.ServiceQuery = sq

	r := mocks.NewRunner(s.T())
	// Only 2 active mons
	r.On("RunCommand", "ceph", "-s", "-f", "json").Return(
		`{"quorum_names":["n0","n1"]}`, nil).Once()
	r.On("RunCommand", "ceph", "mgr", "dump", "-f", "json").Return(
		`{"active_name":"n0","standbys":[{"name":"n1"}]}`, nil).Once()
	r.On("RunCommand", "ceph", "fs", "status", "-f", "json").Return(
		`{"mdsmap":[{"name":"n0"},{"name":"n1"}]}`, nil).Once()
	common.ProcessExec = r

	ops := CheckNonOsdSvcEnoughOps{ClusterOps: ClusterOps{nil, context.Background()}}
	err := ops.Run("n0")
	assert.ErrorContains(s.T(), err, "Insufficient non OSD services")
}

// TestCheckNonOsdSvcEnoughOpsPassTargetNoMon tests the case where the target node
// has no MON. All 3 MONs remain active after removing n3. Pass.
func (s *operationsSuite) TestCheckNonOsdSvcEnoughOpsPassTargetNoMon() {
	sq := mocks.NewServiceQueryInterface(s.T())
	sq.On("List", mock.Anything, mock.Anything).Return(
		types.Services{
			{Service: "mon", Location: "n0"},
			{Service: "mon", Location: "n1"},
			{Service: "mon", Location: "n2"},
		}, nil).Once()
	database.ServiceQuery = sq

	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "-s", "-f", "json").Return(
		`{"quorum_names":["n0","n1","n2"]}`, nil).Once()
	r.On("RunCommand", "ceph", "mgr", "dump", "-f", "json").Return(
		`{"active_name":"n0","standbys":[{"name":"n1"}]}`, nil).Once()
	r.On("RunCommand", "ceph", "fs", "status", "-f", "json").Return(
		`{"mdsmap":[{"name":"n0"},{"name":"n1"}]}`, nil).Once()
	common.ProcessExec = r

	// Target n3 has no MON, so all 3 active MONs remain
	ops := CheckNonOsdSvcEnoughOps{ClusterOps: ClusterOps{nil, context.Background()}}
	err := ops.Run("n3")
	assert.NoError(s.T(), err)
}

// TestCheckNonOsdSvcEnoughOpsErrorServiceQuery tests error from ServiceQuery.List.
func (s *operationsSuite) TestCheckNonOsdSvcEnoughOpsErrorServiceQuery() {
	sq := mocks.NewServiceQueryInterface(s.T())
	sq.On("List", mock.Anything, mock.Anything).Return(
		types.Services(nil), fmt.Errorf("db connection failed")).Once()
	database.ServiceQuery = sq

	ops := CheckNonOsdSvcEnoughOps{ClusterOps: ClusterOps{nil, context.Background()}}
	err := ops.Run("n0")
	assert.ErrorContains(s.T(), err, "error listing services")
}

// TestCheckNonOsdSvcEnoughOpsErrorGetActiveMons tests error from getActiveMons.
func (s *operationsSuite) TestCheckNonOsdSvcEnoughOpsErrorGetActiveMons() {
	sq := mocks.NewServiceQueryInterface(s.T())
	sq.On("List", mock.Anything, mock.Anything).Return(
		types.Services{
			{Service: "mon", Location: "n0"},
			{Service: "mon", Location: "n1"},
			{Service: "mon", Location: "n2"},
		}, nil).Once()
	database.ServiceQuery = sq

	r := mocks.NewRunner(s.T())
	// getActiveMons fails
	r.On("RunCommand", "ceph", "-s", "-f", "json").Return(
		"", fmt.Errorf("ceph not reachable")).Once()
	common.ProcessExec = r

	ops := CheckNonOsdSvcEnoughOps{ClusterOps: ClusterOps{nil, context.Background()}}
	err := ops.Run("n0")
	assert.ErrorContains(s.T(), err, "Error getting active mon")
}
