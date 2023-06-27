package ceph

import (
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microcluster/state"
	"github.com/lxc/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"testing"
)

// osdSuite is the test suite for adding OSDs.
type osdSuite struct {
	baseSuite
	TestStateInterface *mocks.StateInterface
}

func TestOSD(t *testing.T) {
	suite.Run(t, new(osdSuite))
}

// Expect: run ceph osd crush rule ls
func addCrushRuleLsExpectations(r *mocks.Runner) {
	r.On("RunCommand", cmdAny("ceph", 4)...).Return("ok", nil).Once()
}

// Expect: run ceph osd crush rule create-replicated
func addCrushRuleCreateExpectations(r *mocks.Runner) {
	r.On("RunCommand", cmdAny("ceph", 7)...).Return("ok", nil).Once()
}

// Expect: run ceph osd crush rule dump
func addCrushRuleDumpExpectations(r *mocks.Runner) {
	json := `{ "rule_id": 77 }`

	r.On("RunCommand", cmdAny("ceph", 5)...).Return(json, nil).Once()
}

// Expect: run ceph osd crush rule ls json
func addCrushRuleLsJsonExpectations(r *mocks.Runner) {
	json := `[{
        "crush_rule": 77
        "pool_name": "foopool",
    }]`
	r.On("RunCommand", cmdAny("ceph", 5)...).Return(json, nil).Once()
}

// Expect: run ceph osd pool set
func addOsdPoolSetExpectations(r *mocks.Runner) {
	r.On("RunCommand", cmdAny("ceph", 6)...).Return("ok", nil).Once()
}

func (s *osdSuite) SetupTest() {

	s.baseSuite.SetupTest()
	s.copyCephConfigs()

}

// TestSetHostFailureDomain tests the setHostFailureDomain function
func (s *osdSuite) TestSetHostFailureDomain() {
	r := mocks.NewRunner(s.T())
	addCrushRuleLsExpectations(r)
	addCrushRuleCreateExpectations(r)
	addCrushRuleDumpExpectations(r)
	addCrushRuleLsJsonExpectations(r)
	addOsdPoolSetExpectations(r)

	processExec = r

	err := setHostFailureDomain()
	assert.NoError(s.T(), err)
}

// TestUpdateFailureDomain tests the updateFailureDomain function
func (s *osdSuite) TestUpdateFailureDomain() {
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

	r := mocks.NewRunner(s.T())
	addCrushRuleLsExpectations(r)
	addCrushRuleCreateExpectations(r)
	addCrushRuleDumpExpectations(r)
	addCrushRuleLsJsonExpectations(r)
	addOsdPoolSetExpectations(r)
	processExec = r

	c := mocks.NewMemberCounterInterface(s.T())
	c.On("Count", mock.Anything).Return(3, nil).Once()
	database.MemberCounter = c

	err := updateFailureDomain(state)
	assert.NoError(s.T(), err)

}
