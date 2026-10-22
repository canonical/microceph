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

	// Keep the legacy bootstrap tests deterministic: AES-256 support probing is
	// exercised separately, by overriding this function.
	originalSupportsAES256 := common.SupportsAES256Func
	common.SupportsAES256Func = func() (bool, error) {
		return false, nil
	}
	s.T().Cleanup(func() { common.SupportsAES256Func = originalSupportsAES256 })


	s.TestStateInterface = mocks.NewStateInterface(s.T())
	u := api.NewURL()
	u.Host("1.1.1.1")
	state := &mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
	}
	s.TestStateInterface.On("ClusterState").Return(state).Maybe()
}

func addNetworkSimpleBootstrapExpectations(nw *mocks.NetworkIntf) {
	nw.On("FindNetworkAddress", "1.1.1.1").Return("1.1.1.1/24", nil)
	nw.On("IsIpOnSubnet", "1.1.1.1", "1.1.1.1/24").Return(true)
	nw.On("FindIpOnSubnet", "1.1.1.1/24").Return("1.1.1.1", nil)
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
	// crush rule ls (osd, host, rack)
	r.On("RunCommand", tests.CmdAny("ceph", 4)...).Return("ok", nil).Times(3)
	// crush rule create-replicated (osd, host, rack)
	r.On("RunCommand", tests.CmdAny("ceph", 7)...).Return("ok", nil).Times(3)
	// crush rule dump
	r.On("RunCommand", tests.CmdAny("ceph", 5)...).Return("{\"rule_id\": 1}", nil).Once()
	// crush rule set default
	r.On("RunCommand", tests.CmdAny("ceph", 7)...).Return("ok", nil).Once()
}

// Expect: config set for public and cluster networks
func addConfigExpectations(r *mocks.Runner) {
	r.On("RunCommand", tests.CmdAny("ceph", 7)...).Return("ok", nil).Times(3)
}

// TestSimplePrecheckRejectsInvalidAZ verifies that Precheck fails for invalid AZ names.
func (s *simpleBootstrapSuite) TestSimplePrecheckRejectsInvalidAZ() {
	nw := mocks.NewNetworkIntf(s.T())
	addNetworkSimpleBootstrapExpectations(nw)
	common.Network = nw

	bootstrapper := SimpleBootstrapper{
		MonIP:            "1.1.1.1",
		PublicNet:        "1.1.1.1/24",
		ClusterNet:       "1.1.1.1/24",
		AvailabilityZone: "invalid zone!",
	}

	err := bootstrapper.Precheck(context.Background(), s.TestStateInterface)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "invalid availability zone name")
}

// ##### Unit Tests #####
func (s *simpleBootstrapSuite) TestSimpleBootstrap() {
	r := mocks.NewRunner(s.T())
	nw := mocks.NewNetworkIntf(s.T())
	getServicesAndConfigsforDBUpdation = func(_ string, _ string, _ *SimpleBootstrapper) ([]string, map[string]string, []ceph.BootstrapHostTag, error) {
		return []string{}, map[string]string{}, nil, nil
	}

	addNetworkSimpleBootstrapExpectations(nw)
	addCephAuthToolExpectations(r)
	addMonMapToolExpectations(r)
	addInitMonExpectations(r)
	addInitMgrExpectations(r)
	addInitMdsExpectations(r)
	addEnableMsgr2Expectations(r)
	addCrushRuleExpectations(r)
	addConfigExpectations(r)

	common.ProcessExec = r
	common.Network = nw

	bd := common.BootstrapConfig{
		MonIp:      "1.1.1.1",
		PublicNet:  "1.1.1.1/24",
		ClusterNet: "1.1.1.1/24",
	}

	bootstrapper := SimpleBootstrapper{}
	err := bootstrapper.Prefill(bd, interfaces.StateInterface(s.TestStateInterface))
	assert.NoError(s.T(), err)

	err = bootstrapper.Precheck(context.Background(), s.TestStateInterface)
	assert.NoError(s.T(), err)

	err = bootstrapper.Bootstrap(context.Background(), s.TestStateInterface)
	assert.NoError(s.T(), err)
}

// TestPrefillDefaultCipher verifies that the bootstrap prefill resolves an
// omitted cipher to AES-256 when supported and otherwise leaves Ceph's
// defaults untouched.
func (s *simpleBootstrapSuite) TestPrefillDefaultCipher() {
	tests := []struct {
		name      string
		supported bool
		requested string
		expected  string
	}{
		{name: "auto selects AES-256", supported: true, expected: common.CipherAES256},
		{name: "auto leaves defaults when unsupported", supported: false, expected: ""},
		{name: "explicit AES", supported: true, requested: "aes", expected: common.CipherAES},
		{name: "explicit AES-256", supported: true, requested: "aes-256", expected: common.CipherAES256},
	}

	original := common.SupportsAES256Func
	defer func() { common.SupportsAES256Func = original }()

	for _, tc := range tests {
		s.Run(tc.name, func() {
			common.SupportsAES256Func = func() (bool, error) {
				return tc.supported, nil
			}

			bootstrapper := SimpleBootstrapper{}
			err := bootstrapper.Prefill(common.BootstrapConfig{
				MonIp:         "1.1.1.1",
				PublicNet:     "1.1.1.1/24",
				ClusterNet:    "1.1.1.1/24",
				DefaultCipher: tc.requested,
			}, interfaces.StateInterface(s.TestStateInterface))
			s.NoError(err)
			s.Equal(tc.expected, bootstrapper.DefaultCipher)
		})
	}
}
