package main

import (
	"context"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type simpleBootstrapSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestSimpleBootstrap(t *testing.T) {
	suite.Run(t, new(simpleBootstrapSuite))
}

func (s *simpleBootstrapSuite) SetupTest() {
	s.BaseSuite.SetupTest()
	s.CopyCephConfigs()

	s.TestStateInterface = mocks.NewStateInterface(s.T())
	u := api.NewURL()
	u.Host("1.1.1.1")
	state := &mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
	}
	s.TestStateInterface.On("ClusterState").Return(state).Maybe()
}

// Expect: run ceph-authtool 3 times
func addCephAuthToolExpectations(r *mocks.Runner) {
	r.On("RunCommand", tests.CmdAny("ceph-authtool", 8)...).Return("ok", nil).Once()
	r.On("RunCommand", tests.CmdAny("ceph-authtool", 17)...).Return("ok", nil).Once()
	r.On("RunCommand", tests.CmdAny("ceph-authtool", 3)...).Return("ok", nil).Once()
}

// Expect: run monmaptool 2 times
func addMonMapToolExpectations(r *mocks.Runner) {
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

func addCrushRuleExpectations(r *mocks.Runner) {
	// crush rule ls
	r.On("RunCommand", tests.CmdAny("ceph", 4)...).Return("ok", nil).Twice()
	// crush rule create-replicated microceph_auto_osd
	r.On("RunCommand", tests.CmdAny("ceph", 7)...).Return("ok", nil).Twice()
	// crush rule dump
	r.On("RunCommand", tests.CmdAny("ceph", 5)...).Return("{\"rule_id\": 1}", nil).Once()
	// crush rule set default
	r.On("RunCommand", tests.CmdAny("ceph", 7)...).Return("ok", nil).Twice()
}

// Expect: config set for public and cluster networks
func addConfigExpectations(r *mocks.Runner) {
	r.On("RunCommand", tests.CmdAny("ceph", 7)...).Return("ok", nil).Once()
}

// Expect: check network coherency
func addNetworkExpectationsBootstrap(nw *mocks.NetworkIntf, _ interfaces.StateInterface) {
	nw.On("IsIpOnSubnet", "1.1.1.1", "1.1.1.1/24").Return(true)
}

// ##### Unit Tests #####
func (s *simpleBootstrapSuite) TestSimpleBootstrap() {
	r := mocks.NewRunner(s.T())
	nw := mocks.NewNetworkIntf(s.T())
	ceph.PopulateBootstrapDatabase = func(ctx context.Context, s interfaces.StateInterface, monIp string, publicNet string, clusterNet string) error {
		return nil
	}

	ceph.UpdateConfig = func(ctx context.Context, s interfaces.StateInterface) error {
		return nil
	}

	addCephAuthToolExpectations(r)
	addMonMapToolExpectations(r)
	addInitMonExpectations(r)
	addInitMgrExpectations(r)
	addInitMdsExpectations(r)
	addEnableMsgr2Expectations(r)
	addCrushRuleExpectations(r)
	addConfigExpectations(r)
	// addNetworkExpectationsBootstrap(nw, s.TestStateInterface)

	common.ProcessExec = r
	common.Network = nw

	bd := common.BootstrapConfig{
		MonIp:      "1.1.1.1",
		PublicNet:  "1.1.1.1/24",
		ClusterNet: "1.1.1.1/24",
	}

	bootstraper := SimpleBootstraper{}
	bootstraper.Prefill(bd)

	err := bootstraper.Bootstrap(context.Background(), s.TestStateInterface)
	assert.NoError(s.T(), err)
}
