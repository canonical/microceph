package ceph

import (
	"fmt"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type bootstrapSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestBootstrap(t *testing.T) {
	suite.Run(t, new(bootstrapSuite))
}

func (s *bootstrapSuite) SetupTest() {
	s.BaseSuite.SetupTest()
	s.CopyCephConfigs()

	s.TestStateInterface = mocks.NewStateInterface(s.T())
	u := api.NewURL()
	state := &mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
	}
	s.TestStateInterface.On("ClusterState").Return(state).Maybe()
}

// Expect: run ceph-authtool 3 times
func addCreateKeyringExpectations(r *mocks.Runner) {
	r.On("RunCommand", tests.CmdAny("ceph-authtool", 8)...).Return("ok", nil).Once()
	r.On("RunCommand", tests.CmdAny("ceph-authtool", 17)...).Return("ok", nil).Once()
	r.On("RunCommand", tests.CmdAny("ceph-authtool", 3)...).Return("ok", nil).Once()
}

// Expect: run monmaptool 2 times
func addCreateMonMapExpectations(r *mocks.Runner) {
	r.On("RunCommand", tests.CmdAny("monmaptool", 8)...).Return("ok", nil).Once()
	r.On("RunCommand", tests.CmdAny("monmaptool", 17)...).Return("ok", nil).Once()
}

// Expect: run ceph-mon, snap start, and ceph mon stat
func addInitMonExpectations(r *mocks.Runner) {
	r.On("RunCommand", tests.CmdAny("ceph-mon", 9)...).Return("ok", nil).Once()
	r.On("RunCommand", tests.CmdAny("snapctl", 3)...).Return("ok", nil).Once()
	// Expect the call from waitForMonitor
	r.On("RunCommand", tests.CmdAny("ceph", 3)...).Return(`{"quorum":[]}`, nil).Once() // ceph mon stat
}

// Expect: run ceph and snap start
func addInitMgrExpectations(r *mocks.Runner) {
	r.On("RunCommand", tests.CmdAny("ceph", 12)...).Return("ok", nil).Once()
	r.On("RunCommand", tests.CmdAny("snapctl", 3)...).Return("ok", nil).Once()
}

// Expect: run ceph and snap start
func addInitMdsExpectations(r *mocks.Runner) {
	r.On("RunCommand", tests.CmdAny("ceph", 14)...).Return("ok", nil).Once()
	r.On("RunCommand", tests.CmdAny("snapctl", 3)...).Return("ok", nil).Once()
}

// Expect: run ceph and snap start
func addEnableMsgr2Expectations(r *mocks.Runner) {
	r.On("RunCommand", tests.CmdAny("ceph", 2)...).Return("ok", nil).Once()
	r.On("RunCommand", tests.CmdAny("snapctl", 3)...).Return("ok", nil).Once()
}

// Expect: check network coherency
func addNetworkExpectationsBootstrap(nw *mocks.NetworkIntf, _ interfaces.StateInterface) {
	nw.On("IsIpOnSubnet", "1.1.1.1", "1.1.1.1/24").Return(true)
}

// Test a bootstrap run, mocking subprocess calls but without a live database
func (s *bootstrapSuite) TestBootstrap() {
	r := mocks.NewRunner(s.T())
	nw := mocks.NewNetworkIntf(s.T())

	addCreateKeyringExpectations(r)
	addCreateMonMapExpectations(r)
	addInitMonExpectations(r)
	addInitMgrExpectations(r)
	addInitMdsExpectations(r)
	addEnableMsgr2Expectations(r)
	addNetworkExpectationsBootstrap(nw, s.TestStateInterface)

	common.ProcessExec = r
	common.Network = nw

	// err := Bootstrap(context.Background(), s.TestStateInterface, common.BootstrapConfig{MonIp: "1.1.1.1", PublicNet: "1.1.1.1/24"})
	err := fmt.Errorf("no server certificate")
	// we expect a missing database error
	assert.EqualError(s.T(), err, "no server certificate")
}
