package ceph

import (
	"context"
	"fmt"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

// osdSuite is the test suite for adding OSDs.
type osdSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestOSD(t *testing.T) {
	suite.Run(t, new(osdSuite))
}

// Expect: run ceph osd crush rule ls
func addCrushRuleLsExpectations(r *mocks.Runner) {
	r.On("RunCommand", tests.CmdAny("ceph", 4)...).Return("microceph_auto_osd", nil).Once()
}

// Expect: run ceph osd crush rule dump
func addCrushRuleDumpExpectations(r *mocks.Runner) {
	json := `{ "rule_id": 77 }`

	r.On("RunCommand", tests.CmdAny("ceph", 5)...).Return(json, nil).Once()
}

// Expect: run ceph osd crush rule ls json
func addCrushRuleLsJsonExpectations(r *mocks.Runner) {
	json := `[{
        "crush_rule": 77
        "pool_name": "foopool",
    }]`
	r.On("RunCommand", tests.CmdAny("ceph", 5)...).Return(json, nil).Once()
}

// Expect: run ceph osd pool set
func addOsdPoolSetExpectations(r *mocks.Runner) {
	r.On("RunCommand", tests.CmdAny("ceph", 6)...).Return("ok", nil).Once()
}

// Expect: run ceph config set
func addSetDefaultRuleExpectations(r *mocks.Runner) {
	r.On("RunCommand", tests.CmdAny("ceph", 7)...).Return("ok", nil).Once()
}

// Expect: run ceph osd tree
func addOsdTreeExpectations(r *mocks.Runner) {
	json := `{
   "nodes" : [
      {
         "children" : [
            -4,
            -3,
            -2
         ],
         "id" : -1,
         "name" : "default",
         "type" : "root",
         "type_id" : 11
      },
      {
         "children" : [
            0
         ],
         "id" : -2,
         "name" : "m-0",
         "pool_weights" : {},
         "type" : "host",
         "type_id" : 1
      },
      {
         "crush_weight" : 0.0035858154296875,
         "depth" : 2,
         "exists" : 1,
         "id" : 0,
         "name" : "osd.0",
         "pool_weights" : {},
         "primary_affinity" : 1,
         "reweight" : 1,
         "status" : "up",
         "type" : "osd",
         "type_id" : 0
      }
  ], "stray" : [{ "id": 77,
          "name": "osd.77",
          "exists": 1} ]}`
	r.On("RunCommand", tests.CmdAny("ceph", 4)...).Return(json, nil).Once()

}

func addSetOsdStateUpExpectations(r *mocks.Runner) {
	r.On("RunCommand", "snapctl", "start", "microceph.osd", "--enable").Return("ok", nil).Once()
}

func addSetOsdStateDownExpectations(r *mocks.Runner) {
	r.On("RunCommand", "snapctl", "stop", "microceph.osd", "--disable").Return("ok", nil).Once()
}

func addSetOsdStateUpFailedExpectations(r *mocks.Runner) {
	r.On("RunCommand", "snapctl", "start", "microceph.osd", "--enable").Return("fail", fmt.Errorf("some errors")).Once()
}

func addSetOsdStateDownFailedExpectations(r *mocks.Runner) {
	r.On("RunCommand", "snapctl", "stop", "microceph.osd", "--disable").Return("fail", fmt.Errorf("some errors")).Once()
}

func addOsdtNooutFlagTrueExpectations(r *mocks.Runner) {
	r.On("RunCommand", "ceph", "osd", "set", "noout").Return("ok", nil).Once()
}

func addOsdtNooutFlagFalseExpectations(r *mocks.Runner) {
	r.On("RunCommand", "ceph", "osd", "unset", "noout").Return("ok", nil).Once()
}

func addOsdtNooutFlagFailedExpectations(r *mocks.Runner) {
	r.On("RunCommand", "ceph", "osd", "set", "noout").Return("fail", fmt.Errorf("some errors")).Once()
}

func addIsOsdNooutSetTrueExpections(r *mocks.Runner) {
	r.On("RunCommand", "ceph", "osd", "dump").Return("flags sortbitwise,noout", nil).Once()
}

func addIsOsdNooutSetFalseExpections(r *mocks.Runner) {
	r.On("RunCommand", "ceph", "osd", "dump").Return("flags sortbitwise", nil).Once()
}

func addIsOsdNooutSetFailedExpections(r *mocks.Runner) {
	r.On("RunCommand", "ceph", "osd", "dump").Return("fail", fmt.Errorf("some errors")).Once()
}

func (s *osdSuite) SetupTest() {

	s.BaseSuite.SetupTest()
	s.CopyCephConfigs()

}

// TestSwitchHostFailureDomain tests the switchFailureDomain function
func (s *osdSuite) TestSwitchHostFailureDomain() {
	r := mocks.NewRunner(s.T())

	// dump crush rules to resolve names
	addCrushRuleDumpExpectations(r)
	// set default crush rule
	addSetDefaultRuleExpectations(r)
	// list to check if crush rule exists
	addCrushRuleLsExpectations(r)
	// dump crush rules to resolve names
	addCrushRuleDumpExpectations(r)
	// list pools
	addCrushRuleLsJsonExpectations(r)
	// set pool crush rule
	addOsdPoolSetExpectations(r)

	processExec = r

	err := switchFailureDomain("osd", "host")
	assert.NoError(s.T(), err)
}

// TestUpdateFailureDomain tests the updateFailureDomain function
func (s *osdSuite) TestUpdateFailureDomain() {
	u := api.NewURL()
	state := &mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
	}

	r := mocks.NewRunner(s.T())

	// dump crush rules to resolve names
	addCrushRuleDumpExpectations(r)
	// set default crush rule
	addSetDefaultRuleExpectations(r)
	// list to check if crush rule exists
	addCrushRuleLsExpectations(r)
	// dump crush rules to resolve names
	addCrushRuleDumpExpectations(r)
	// list pools
	addCrushRuleLsJsonExpectations(r)
	// set pool crush rule
	addOsdPoolSetExpectations(r)

	processExec = r

	c := mocks.NewMemberCounterInterface(s.T())
	c.On("Count", mock.Anything).Return(3, nil).Once()
	database.MemberCounter = c

	s.TestStateInterface = mocks.NewStateInterface(s.T())
	s.TestStateInterface.On("ClusterState").Return(state).Maybe()
	err := updateFailureDomain(context.Background(), s.TestStateInterface.ClusterState())
	assert.NoError(s.T(), err)

}

// TestHaveOSDInCeph tests the haveOSDInCeph function
func (s *osdSuite) TestHaveOSDInCeph() {
	r := mocks.NewRunner(s.T())
	// add osd tree expectations
	addOsdTreeExpectations(r)
	addOsdTreeExpectations(r)

	processExec = r

	res, err := haveOSDInCeph(0)
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), res, true)

	res, err = haveOSDInCeph(77)
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), res, false)

}

// TestSetOsdStateOkay tests the SetOsdState function when no error occurs
func (s *osdSuite) TestSetOsdStateOkay() {
	r := mocks.NewRunner(s.T())
	addSetOsdStateUpExpectations(r)
	addSetOsdStateDownExpectations(r)

	// patch processExec
	processExec = r

	err := SetOsdState(true)
	assert.NoError(s.T(), err)

	err = SetOsdState(false)
	assert.NoError(s.T(), err)
}

// TestSetOsdStateFail tests the SetOsdState function when error occurs
func (s *osdSuite) TestSetOsdStateFail() {
	r := mocks.NewRunner(s.T())
	addSetOsdStateUpFailedExpectations(r)
	addSetOsdStateDownFailedExpectations(r)

	// patch processExec
	processExec = r

	err := SetOsdState(true)
	assert.Error(s.T(), err)

	err = SetOsdState(false)
	assert.Error(s.T(), err)
}

// TestOsdNooutFlagOkay tests the osdNooutFlag function when no error occurs
func (s *osdSuite) TestOsdNooutFlagOkay() {
	r := mocks.NewRunner(s.T())
	addOsdtNooutFlagTrueExpectations(r)
	addOsdtNooutFlagFalseExpectations(r)

	// patch processExec
	processExec = r

	err := osdNooutFlag(true)
	assert.NoError(s.T(), err)

	err = osdNooutFlag(false)
	assert.NoError(s.T(), err)
}

// TestOsdNooutFlagFail tests the osdNooutFlag function when error occurs
func (s *osdSuite) TestOsdNooutFlagFail() {
	r := mocks.NewRunner(s.T())
	addOsdtNooutFlagFailedExpectations(r)

	// patch processExec
	processExec = r

	err := osdNooutFlag(true)
	assert.Error(s.T(), err)
}

// TestIsOsdNooutSetOkay tests the isOsdNooutSet function when no error occurs
func (s *osdSuite) TestIsOsdNooutSetOkay() {
	r := mocks.NewRunner(s.T())
	addIsOsdNooutSetTrueExpections(r)
	addIsOsdNooutSetFalseExpections(r)

	// patch processExec
	processExec = r

	// noout is set
	set, err := isOsdNooutSet()
	assert.True(s.T(), set)
	assert.NoError(s.T(), err)

	// noout is not set
	set, err = isOsdNooutSet()
	assert.False(s.T(), set)
	assert.NoError(s.T(), err)
}

// TestIsOsdNooutSetFail tests the isOsdNooutSet function when error occurs
func (s *osdSuite) TestIsOsdNooutSetFail() {
	r := mocks.NewRunner(s.T())
	addIsOsdNooutSetFailedExpections(r)

	// patch processExec
	processExec = r

	// error running ceph osd dump
	set, err := isOsdNooutSet()
	assert.False(s.T(), set)
	assert.Error(s.T(), err)
}
