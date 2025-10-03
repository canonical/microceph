package main

import (
	"context"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type adoptBootstrapSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestAdoptBootstrap(t *testing.T) {
	suite.Run(t, new(adoptBootstrapSuite))
}

func (s *adoptBootstrapSuite) SetupTest() {
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

// addNetworkExpectations sets up the network mock expectations for AdoptBootstrapper tests
func addNetworkExpectations(nw *mocks.NetworkIntf) {
	nw.On("FindIpOnSubnet", "1.1.1.1/24").Return("1.1.1.1", nil)
	nw.On("IsIpOnSubnet", "1.1.1.1", "1.1.1.1/24").Return(true)
	nw.On("FindNetworkAddress", "1.1.1.1").Return("1.1.1.1/24", nil)
}

func addCephConnectivityCheckExpectations(r *mocks.Runner) {
	// ceph status -c $path -k $path -m $monIP should return ok
	r.On("RunCommand", tests.CmdAny("ceph", 9)...).Return(`{"health":{"status":"HEALTH_OK"}}`, nil).Once()
}

// Expect: ceph config calls
func addConfigAdoptExpectations(r *mocks.Runner) {
	// ceph config get public_network should return no config
	r.On("RunCommand", tests.CmdAny("ceph", 4)...).Return("", nil).Once()

	// which causes to set the microceph deduced public network
	r.On("RunCommand", tests.CmdAny("ceph", 7)...).Return("", nil).Once()

	// ceph config get cluster_network should return no config
	r.On("RunCommand", tests.CmdAny("ceph", 4)...).Return("", nil).Once()

	// which causes to set the microceph deduced cluster_network
	r.On("RunCommand", tests.CmdAny("ceph", 7)...).Return("", nil).Once()
}

// ##### Unit Tests #####
func (s *adoptBootstrapSuite) TestAdoptBootstrap() {
	r := mocks.NewRunner(s.T())
	nw := mocks.NewNetworkIntf(s.T())

	getConfigsforDBUpdation = func(_ *AdoptBootstrapper) (map[string]string, error) {
		return map[string]string{}, nil
	}

	addNetworkExpectations(nw)
	addCephConnectivityCheckExpectations(r)
	addConfigAdoptExpectations(r)

	common.ProcessExec = r
	common.Network = nw

	bd := common.BootstrapConfig{
		PublicNet:     "1.1.1.1/24",
		ClusterNet:    "1.1.1.1/24",
		AdoptFSID:     "abcdefgh",
		AdoptMonHosts: []string{"1.1.1.12"},
		AdoptAdminKey: "AQ",
	}

	bootstrapper := AdoptBootstrapper{}
	err := bootstrapper.Prefill(bd, interfaces.StateInterface(s.TestStateInterface))
	assert.NoError(s.T(), err)

	err = bootstrapper.Precheck(context.Background(), s.TestStateInterface)
	assert.NoError(s.T(), err)

	err = bootstrapper.Bootstrap(context.Background(), s.TestStateInterface)
	assert.NoError(s.T(), err)
}
