package ceph

import (
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microcluster/state"
	"github.com/lxc/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"testing"
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
	s.TestStateInterface.On("ClusterState").Return(state)
}

// Test a bootstrap run, mocking subprocess calls but without a live database
func (s *bootstrapSuite) TestBootstrap() {
	r := mocks.NewRunner(s.T())
	addCreateKeyringExpectations(r)
	addCreateMonMapExpectations(r)
	addInitMonExpectations(r)
	addInitMgrExpectations(r)
	addInitMdsExpectations(r)
	addEnableMsgr2Expectations(r)

	processExec = r

	err := Bootstrap(s.TestStateInterface)

	// we expect a missing database error
	assert.EqualError(s.T(), err, "no database")

}
