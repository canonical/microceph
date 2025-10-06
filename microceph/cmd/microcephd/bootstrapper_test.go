package main

import (
	"fmt"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
	"github.com/google/go-cmp/cmp"
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
	u.Host("1.1.1.1")
	state := &mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
	}
	s.TestStateInterface.On("ClusterState").Return(state).Maybe()
}

// ##### Expectation/ Mockers #####

func addNetworkExpectationsCaseOne(nw *mocks.NetworkIntf, _ interfaces.StateInterface) {
	nw.On("FindNetworkAddress", "1.1.1.1").Return("1.1.1.1/24", nil)
	nw.On("IsIpOnSubnet", "1.1.1.1", "1.1.1.1/24").Return(true)
	nw.On("FindIpOnSubnet", "1.1.1.1/24").Return("1.1.1.1", nil)
}

func addNetworkExpectationsCaseTwo(nw *mocks.NetworkIntf, _ interfaces.StateInterface) {
	nw.On("IsIpOnSubnet", "1.1.1.1", "1.1.1.1/24").Return(true)
	nw.On("FindIpOnSubnet", "1.1.1.1/24").Return("1.1.1.1", nil)
	nw.On("FindNetworkAddress", "1.1.1.1").Return("1.1.1.1/24", nil)
	nw.On("FindIpOnSubnet", "2.1.1.1/24").Return("2.1.1.1", nil)
}

func addNetworkExpectationsCaseThree(nw *mocks.NetworkIntf, _ interfaces.StateInterface) {
	nw.On("FindNetworkAddress", "1.1.1.1").Return("1.1.1.1/24", nil)
	nw.On("IsIpOnSubnet", "1.1.1.1", "2.1.1.1/24").Return(false)
}

// ##### Unit Tests #####

// Case 1. Only mon-ip is provided.
func (s *bootstrapSuite) TestSimpleBootstrapOnlyMonIP() {
	r := mocks.NewRunner(s.T())
	nw := mocks.NewNetworkIntf(s.T())

	addNetworkExpectationsCaseOne(nw, s.TestStateInterface)

	common.ProcessExec = r
	common.Network = nw

	bootstrapData := common.BootstrapConfig{MonIp: "1.1.1.1"}

	err := PopulateDefaultNetworkParams(s.TestStateInterface, &bootstrapData.MonIp, &bootstrapData.PublicNet, &bootstrapData.ClusterNet)
	assert.NoError(s.T(), err)

	err = ValidateNetworkParams(s.TestStateInterface, &bootstrapData.MonIp, &bootstrapData.PublicNet, &bootstrapData.ClusterNet)
	assert.NoError(s.T(), err)
	assert.True(s.T(), cmp.Equal(bootstrapData, common.BootstrapConfig{
		MonIp:      "1.1.1.1",
		PublicNet:  "1.1.1.1/24",
		ClusterNet: "1.1.1.1/24",
	}))
}

// Case 2: Cluster and Public networks are provided.
func (s *bootstrapSuite) TestSimpleBootstrapOnlyNet() {
	r := mocks.NewRunner(s.T())
	nw := mocks.NewNetworkIntf(s.T())

	addNetworkExpectationsCaseTwo(nw, s.TestStateInterface)

	common.ProcessExec = r
	common.Network = nw
	bootstrapData := common.BootstrapConfig{PublicNet: "1.1.1.1/24", ClusterNet: "2.1.1.1/24"}

	err := PopulateDefaultNetworkParams(s.TestStateInterface, &bootstrapData.MonIp, &bootstrapData.PublicNet, &bootstrapData.ClusterNet)
	assert.NoError(s.T(), err)

	err = ValidateNetworkParams(s.TestStateInterface, &bootstrapData.MonIp, &bootstrapData.PublicNet, &bootstrapData.ClusterNet)
	assert.NoError(s.T(), err)
	assert.True(s.T(), cmp.Equal(bootstrapData, common.BootstrapConfig{
		MonIp:      "1.1.1.1",
		PublicNet:  "1.1.1.1/24",
		ClusterNet: "2.1.1.1/24",
	}))
}

// Case 3. Incoherent mon-ip and pubilc-network were provided.
func (s *bootstrapSuite) TestSimpleBootstrapMonIpNotInPubNet() {
	r := mocks.NewRunner(s.T())
	nw := mocks.NewNetworkIntf(s.T())

	addNetworkExpectationsCaseThree(nw, s.TestStateInterface)

	common.ProcessExec = r
	common.Network = nw
	bootstrapData := common.BootstrapConfig{MonIp: "1.1.1.1", PublicNet: "2.1.1.1/24"}

	err := PopulateDefaultNetworkParams(s.TestStateInterface, &bootstrapData.MonIp, &bootstrapData.PublicNet, &bootstrapData.ClusterNet)
	assert.NoError(s.T(), err)

	err = ValidateNetworkParams(s.TestStateInterface, &bootstrapData.MonIp, &bootstrapData.PublicNet, &bootstrapData.ClusterNet)
	assert.ErrorContains(s.T(), err, "is not available on public network")
	assert.True(s.T(), cmp.Equal(bootstrapData, common.BootstrapConfig{
		MonIp:      "1.1.1.1",
		PublicNet:  "2.1.1.1/24",
		ClusterNet: "2.1.1.1/24",
	}))
}

// Case 4. No parameters were provided, but v2Only is set to true.
func (s *bootstrapSuite) TestSimpleBootstrapNoParamsV2Only() {
	r := mocks.NewRunner(s.T())
	nw := mocks.NewNetworkIntf(s.T())

	addNetworkExpectationsCaseOne(nw, s.TestStateInterface)

	common.ProcessExec = r
	common.Network = nw

	bootstrapData := common.BootstrapConfig{V2Only: true}

	err := PopulateDefaultNetworkParams(s.TestStateInterface, &bootstrapData.MonIp, &bootstrapData.PublicNet, &bootstrapData.ClusterNet)
	assert.NoError(s.T(), err)

	PopulateV2OnlyMonIP(&bootstrapData.MonIp, bootstrapData.V2Only)

	monIP := bootstrapData.MonIp
	if bootstrapData.V2Only {
		monIP = StripV2OnlyMonIP(monIP)
	}

	expectedMonIP := fmt.Sprintf("%s%s%s", constants.V2OnlyMonIPProtoPrefix, monIP, constants.V2OnlyMonIPPort)

	err = ValidateNetworkParams(s.TestStateInterface, &monIP, &bootstrapData.PublicNet, &bootstrapData.ClusterNet)
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), bootstrapData, common.BootstrapConfig{
		MonIp:      expectedMonIP,
		PublicNet:  "1.1.1.1/24",
		ClusterNet: "1.1.1.1/24",
		V2Only:     true,
	})

	err = ValidateMonV2Param(s.TestStateInterface, &bootstrapData.MonIp, bootstrapData.V2Only)
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), expectedMonIP, bootstrapData.MonIp)
}
