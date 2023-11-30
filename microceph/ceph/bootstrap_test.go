package ceph

import (
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microcluster/state"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type bootstrapSuite struct {
	baseSuite
	TestStateInterface *mocks.StateInterface
}

func TestBootstrap(t *testing.T) {
	suite.Run(t, new(bootstrapSuite))
}

// Expect: run ceph-authtool 3 times
func addCreateKeyringExpectations(r *mocks.Runner) {
	r.On("RunCommand", cmdAny("ceph-authtool", 8)...).Return("ok", nil).Once()
	r.On("RunCommand", cmdAny("ceph-authtool", 17)...).Return("ok", nil).Once()
	r.On("RunCommand", cmdAny("ceph-authtool", 3)...).Return("ok", nil).Once()
}

// Expect: run monmaptool 2 times
func addCreateMonMapExpectations(r *mocks.Runner) {
	r.On("RunCommand", cmdAny("monmaptool", 8)...).Return("ok", nil).Once()
	r.On("RunCommand", cmdAny("monmaptool", 17)...).Return("ok", nil).Once()
}

// Expect: run ceph-mon and snap start
func addInitMonExpectations(r *mocks.Runner) {
	r.On("RunCommand", cmdAny("ceph-mon", 9)...).Return("ok", nil).Once()
	r.On("RunCommand", cmdAny("snapctl", 3)...).Return("ok", nil).Once()
}

// Expect: run ceph and snap start
func addInitMgrExpectations(r *mocks.Runner) {
	r.On("RunCommand", cmdAny("ceph", 11)...).Return("ok", nil).Once()
	r.On("RunCommand", cmdAny("snapctl", 3)...).Return("ok", nil).Once()
}

// Expect: run ceph and snap start
func addInitMdsExpectations(r *mocks.Runner) {
	r.On("RunCommand", cmdAny("ceph", 13)...).Return("ok", nil).Once()
	r.On("RunCommand", cmdAny("snapctl", 3)...).Return("ok", nil).Once()
}

// Expect: run ceph and snap start
func addEnableMsgr2Expectations(r *mocks.Runner) {
	r.On("RunCommand", cmdAny("ceph", 2)...).Return("ok", nil).Once()
	r.On("RunCommand", cmdAny("snapctl", 3)...).Return("ok", nil).Once()
}

// Expect: check network coherency
func addNetworkExpectationsBootstrap(nw *mocks.NetworkIntf, s common.StateInterface) {
	nw.On("IsIpOnSubnet", "1.1.1.1", "1.1.1.1/24").Return(true)
}

// Expect: check Bootstrap data prep
func addNetworkExpectations(nw *mocks.NetworkIntf, s common.StateInterface) {
	nw.On("IsIpOnSubnet", "1.1.1.1", "1.1.1.1/24").Return(true)
	nw.On("FindNetworkAddress", "1.1.1.1").Return("1.1.1.1/24", nil)
	nw.On("FindIpOnSubnet", "1.1.1.1/24").Return("1.1.1.1", nil)
	// failure case
	nw.On("IsIpOnSubnet", "1.1.1.1", "2.1.1.1/24").Return(false)
}

func (s *bootstrapSuite) SetupTest() {

	s.baseSuite.SetupTest()
	s.copyCephConfigs()

	s.TestStateInterface = mocks.NewStateInterface(s.T())
	u := api.NewURL()
	state := &state.State{
		Address: func() *api.URL {
			return u
		},
		Name: func() string {
			return "foohost"
		},
		Database: nil,
	}
	s.TestStateInterface.On("ClusterState").Return(state).Maybe()
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

	processExec = r
	common.Network = nw

	err := Bootstrap(s.TestStateInterface, common.BootstrapConfig{MonIp: "1.1.1.1", PublicNet: "1.1.1.1/24"})

	// we expect a missing database error
	assert.EqualError(s.T(), err, "no database")

}

// Test prepareCephBootstrapData
func (s *bootstrapSuite) TestBootstrapDataPrep() {
	r := mocks.NewRunner(s.T())
	nw := mocks.NewNetworkIntf(s.T())

	addNetworkExpectations(nw, s.TestStateInterface)

	processExec = r
	common.Network = nw

	// Case 1. Only mon-ip is provided.
	input := common.BootstrapConfig{MonIp: "1.1.1.1"}
	err := prepareCephBootstrapData(s.TestStateInterface, &input)
	assert.NoError(s.T(), err)
	assert.True(s.T(), cmp.Equal(input, common.BootstrapConfig{
		MonIp:      "1.1.1.1",
		PublicNet:  "1.1.1.1/24",
		ClusterNet: "1.1.1.1/24",
	}))

	// Case 2. Only public-network is provided.
	input = common.BootstrapConfig{PublicNet: "1.1.1.1/24"}
	err = prepareCephBootstrapData(s.TestStateInterface, &input)
	assert.NoError(s.T(), err)
	assert.True(s.T(), cmp.Equal(input, common.BootstrapConfig{
		MonIp:      "1.1.1.1",
		PublicNet:  "1.1.1.1/24",
		ClusterNet: "1.1.1.1/24",
	}))

	// Case 3: Cluster network is also provided.
	input = common.BootstrapConfig{PublicNet: "1.1.1.1/24", ClusterNet: "2.1.1.1/24"}
	err = prepareCephBootstrapData(s.TestStateInterface, &input)
	assert.NoError(s.T(), err)
	assert.True(s.T(), cmp.Equal(input, common.BootstrapConfig{
		MonIp:      "1.1.1.1",
		PublicNet:  "1.1.1.1/24",
		ClusterNet: "2.1.1.1/24",
	}))

	// Case 4. Incoherent mon-ip and pubilc-network were provided.
	input = common.BootstrapConfig{MonIp: "1.1.1.1", PublicNet: "2.1.1.1/24"}
	err = prepareCephBootstrapData(s.TestStateInterface, &input)
	assert.ErrorContains(s.T(), err, "is not available on public network")
	assert.True(s.T(), cmp.Equal(input, common.BootstrapConfig{
		MonIp:      "1.1.1.1",
		PublicNet:  "2.1.1.1/24",
		ClusterNet: "",
	}))
}
