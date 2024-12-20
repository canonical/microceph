package ceph

import (
	"fmt"
	"testing"

	"github.com/canonical/microceph/microceph/tests"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
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
	TestStateInterface *mocks.StateInterface
}

func (s *operationsSuite) TestCheckNodeInClusterOpsTrue() {
	m := mocks.NewClientInterface(s.T())
	m.On("GetClusterMembers", mock.Anything).Return([]string{"microceph-0", "microceph-1"}, nil).Once()

	// patch ceph client
	client.MClient = m

	// node microceph-0 is in the cluster
	ops := CheckNodeInClusterOps{client.MClient, nil}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestCheckNodeInClusterOpsFalse() {
	m := mocks.NewClientInterface(s.T())
	m.On("GetClusterMembers", mock.Anything).Return([]string{"microceph-0", "microceph-1"}, nil).Once()

	// patch ceph client
	client.MClient = m

	// node microceph-2 is not in the cluster
	ops := CheckNodeInClusterOps{client.MClient, nil}
	err := ops.Run("microceph-2")
	assert.ErrorContains(s.T(), err, "not found")
}

func (s *operationsSuite) TestCheckNodeInClusterOpsError() {
	m := mocks.NewClientInterface(s.T())
	m.On("GetClusterMembers", mock.Anything).Return([]string{}, fmt.Errorf("some reasons")).Once()

	// patch ceph client
	client.MClient = m

	// cannot get cluster member
	ops := CheckNodeInClusterOps{client.MClient, nil}
	err := ops.Run("some-node-name")
	assert.ErrorContains(s.T(), err, "Error getting cluster members")
}

func (s *operationsSuite) TestCheckOsdOkToStopOpsTrue() {
	m := mocks.NewClientInterface(s.T())
	m.On("GetDisks", mock.Anything).Return(
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

	// patch ceph client
	client.MClient = m

	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "ok-to-stop", "osd.1").Return("ok", nil).Once()

	// patch processExec
	processExec = r

	// osd.1 in microceph-0 is okay to stop
	ops := CheckOsdOkToStopOps{client.MClient, nil}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestCheckOsdOkToStopOpsFalse() {
	m := mocks.NewClientInterface(s.T())
	m.On("GetDisks", mock.Anything).Return(
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

	// patch ceph client
	client.MClient = m

	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "ok-to-stop", "osd.1").Return("fail", fmt.Errorf("some reasons")).Once()

	// patch processExec
	processExec = r

	// osd.1 in microceph-0 is not okay to stop
	ops := CheckOsdOkToStopOps{client.MClient, nil}
	err := ops.Run("microceph-0")
	assert.ErrorContains(s.T(), err, "cannot be safely stopped")
}

func (s *operationsSuite) TestCheckOsdOkToStopOpsError() {
	m := mocks.NewClientInterface(s.T())
	m.On("GetDisks", mock.Anything).Return(types.Disks{}, fmt.Errorf("some reasons")).Once()

	// patch ceph client
	client.MClient = m

	// cannot get disks
	ops := CheckOsdOkToStopOps{client.MClient, nil}
	err := ops.Run("some-node-name")
	assert.ErrorContains(s.T(), err, "Error getting disks")
}

func (s *operationsSuite) TestCheckNonOsdSvcEnoughOpsTrue() {
	m := mocks.NewClientInterface(s.T())
	// 4 mons, 1 mds, 1 mgr
	m.On("GetServices", mock.Anything).Return(
		types.Services{
			{
				Service:  "mon",
				Location: "microceph-0",
			},
			{
				Service:  "mds",
				Location: "microceph-0",
			},
			{
				Service:  "mgr",
				Location: "microceph-0",
			},
			{
				Service:  "mon",
				Location: "microceph-1",
			},
			{
				Service:  "mon",
				Location: "microceph-2",
			},
			{
				Service:  "mon",
				Location: "microceph-3",
			},
		}, nil).Once()

	// patch ceph client
	client.MClient = m

	// microceph-3 go to maintenance mode -> 3 mons, 1 mds, 1 mgr -> ok
	ops := CheckNonOsdSvcEnoughOps{client.MClient, nil, 3, 1, 1}
	err := ops.Run("microceph-3")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestCheckNonOsdSvcEnoughOpsFalse() {
	m := mocks.NewClientInterface(s.T())
	// 4 mons, 1 mds, 1 mgr
	m.On("GetServices", mock.Anything).Return(
		types.Services{
			{
				Service:  "mon",
				Location: "microceph-0",
			},
			{
				Service:  "mds",
				Location: "microceph-0",
			},
			{
				Service:  "mgr",
				Location: "microceph-0",
			},
			{
				Service:  "mon",
				Location: "microceph-1",
			},
			{
				Service:  "mon",
				Location: "microceph-2",
			},
			{
				Service:  "mon",
				Location: "microceph-3",
			},
		}, nil).Once()

	// patch ceph client
	client.MClient = m

	// microceph-0 go to maintenance mode -> 3 mons, 0 mds, 0 mgr -> no ok
	ops := CheckNonOsdSvcEnoughOps{client.MClient, nil, 3, 1, 1}
	err := ops.Run("microceph-0")
	assert.Error(s.T(), err)
}

func (s *operationsSuite) TestCheckNonOsdSvcEnoughOpsError() {
	m := mocks.NewClientInterface(s.T())
	m.On("GetServices", mock.Anything).Return(types.Services{}, fmt.Errorf("some reasons")).Once()

	// patch ceph client
	client.MClient = m

	// cannot get services
	ops := CheckNonOsdSvcEnoughOps{client.MClient, nil, 3, 1, 1}
	err := ops.Run("some-node-name")
	assert.ErrorContains(s.T(), err, "Error getting services")
}

func (s *operationsSuite) TestSetNooutOpsOkay() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "set", "noout").Return("ok", nil).Once()

	// patch processExec
	processExec = r

	ops := SetNooutOps{}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestSetNooutOpsFail() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "set", "noout").Return("fail", fmt.Errorf("some reasons")).Once()

	// patch processExec
	processExec = r

	ops := SetNooutOps{}
	err := ops.Run("microceph-0")
	assert.Error(s.T(), err)
}

func (s *operationsSuite) TestAssertNooutFlagSetOpsTrue() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump").Return("flags noout", nil).Once()

	// patch processExec
	processExec = r

	ops := AssertNooutFlagSetOps{}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestAssertNooutFlagSetOpsFalse() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump").Return("flags", nil).Once()

	// patch processExec
	processExec = r

	ops := AssertNooutFlagSetOps{}
	err := ops.Run("microceph-0")
	assert.ErrorContains(s.T(), err, "unset")
}

func (s *operationsSuite) TestAssertNooutFlagSetOpsError() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump").Return("fail", fmt.Errorf("some reasons")).Once()

	// patch processExec
	processExec = r

	ops := AssertNooutFlagSetOps{}
	err := ops.Run("microceph-0")
	assert.Error(s.T(), err)
}

func (s *operationsSuite) TestAssertNooutFlagUnsetOpsTrue() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump").Return("flags", nil).Once()

	// patch processExec
	processExec = r

	ops := AssertNooutFlagUnsetOps{}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestAssertNooutFlagUnsetOpsFalse() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump").Return("flags noout", nil).Once()

	// patch processExec
	processExec = r

	ops := AssertNooutFlagUnsetOps{}
	err := ops.Run("microceph-0")
	assert.ErrorContains(s.T(), err, "set")
}

func (s *operationsSuite) TestAssertNooutFlagUnsetOpsError() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "dump").Return("fail", fmt.Errorf("some reasons")).Once()

	// patch processExec
	processExec = r

	ops := AssertNooutFlagUnsetOps{}
	err := ops.Run("microceph-0")
	assert.Error(s.T(), err)
}

func (s *operationsSuite) TestStopOsdOpsOkay() {
	m := mocks.NewClientInterface(s.T())
	m.On("PutOsds", mock.Anything, false, mock.Anything).Return(nil)

	// patch ceph client
	client.MClient = m

	ops := StopOsdOps{client.MClient, nil}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestStopOsdOpsFail() {
	m := mocks.NewClientInterface(s.T())
	m.On("PutOsds", mock.Anything, false, mock.Anything).Return(fmt.Errorf("some reasons"))

	// patch ceph client
	client.MClient = m

	ops := StopOsdOps{client.MClient, nil}
	err := ops.Run("microceph-0")
	assert.Error(s.T(), err, "Unable to stop OSD service in node")
}

func (s *operationsSuite) TestStartOsdOpsOkay() {
	m := mocks.NewClientInterface(s.T())
	m.On("PutOsds", mock.Anything, true, mock.Anything).Return(nil)

	// patch ceph client
	client.MClient = m

	ops := StartOsdOps{client.MClient, nil}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestStartOsdOpsFail() {
	m := mocks.NewClientInterface(s.T())
	m.On("PutOsds", mock.Anything, true, mock.Anything).Return(fmt.Errorf("some reasons"))

	// patch ceph client
	client.MClient = m

	ops := StartOsdOps{client.MClient, nil}
	err := ops.Run("microceph-0")
	assert.Error(s.T(), err, "Unable to start OSD service in node")
}

func (s *operationsSuite) TestUnSetNooutOpsOkay() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "unset", "noout").Return("ok", nil).Once()

	// patch processExec
	processExec = r

	ops := UnsetNooutOps{}
	err := ops.Run("microceph-0")
	assert.NoError(s.T(), err)
}

func (s *operationsSuite) TestUnSetNooutOpsFail() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "osd", "unset", "noout").Return("fail", fmt.Errorf("some reasons")).Once()

	// patch processExec
	processExec = r

	ops := UnsetNooutOps{}
	err := ops.Run("microceph-0")
	assert.Error(s.T(), err)
}
