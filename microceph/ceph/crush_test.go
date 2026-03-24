package ceph

import (
	"fmt"
	"testing"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"github.com/tidwall/gjson"
)

type crushSuite struct {
	tests.BaseSuite
}

func TestCrush(t *testing.T) {
	suite.Run(t, new(crushSuite))
}

// mockRackRuleRunner sets up a Runner mock so that IsOnRackRule sees the
// cluster on rack rule (defaultID == rackID).
func mockRackRuleRunner(t *testing.T, defaultID string, rackID string) *mocks.Runner {
	r := mocks.NewRunner(t)
	// getDefaultCrushRule -> GetConfigItem -> ceph config get mon osd_pool_default_crush_rule
	r.On("RunCommand", "ceph", "config", "get", "mon", "osd_pool_default_crush_rule").
		Return(defaultID+"\n", nil).Maybe()
	// getCrushRuleID("microceph_auto_rack") -> ceph osd crush rule dump microceph_auto_rack
	r.On("RunCommand", "ceph", "osd", "crush", "rule", "dump", "microceph_auto_rack").
		Return(fmt.Sprintf(`{"rule_id": %s}`, rackID), nil).Maybe()
	return r
}

func (s *crushSuite) TestIsOnRackRuleTrue() {
	r := mockRackRuleRunner(s.T(), "3", "3")
	common.ProcessExec = r
	onRack, err := IsOnRackRule()
	assert.NoError(s.T(), err)
	assert.True(s.T(), onRack)
}

func (s *crushSuite) TestIsOnRackRuleFalseOnHost() {
	r := mockRackRuleRunner(s.T(), "2", "3")
	common.ProcessExec = r
	onRack, err := IsOnRackRule()
	assert.NoError(s.T(), err)
	assert.False(s.T(), onRack)
}

func (s *crushSuite) TestIsOnRackRuleErrorNoRackRule() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "config", "get", "mon", "osd_pool_default_crush_rule").
		Return("2\n", nil).Maybe()
	r.On("RunCommand", "ceph", "osd", "crush", "rule", "dump", "microceph_auto_rack").
		Return("", fmt.Errorf("rule not found")).Maybe()
	common.ProcessExec = r
	onRack, err := IsOnRackRule()
	assert.Error(s.T(), err)
	assert.False(s.T(), onRack)
}

func (s *crushSuite) TestIsOnRackRuleErrorDefaultRule() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "config", "get", "mon", "osd_pool_default_crush_rule").
		Return("", fmt.Errorf("ceph unreachable")).Maybe()
	common.ProcessExec = r
	onRack, err := IsOnRackRule()
	assert.Error(s.T(), err)
	assert.False(s.T(), onRack)
}

func (s *crushSuite) TestCountOSDsInAZRack() {
	nodes := gjson.Get(osdTreeWithOSDs([]string{"az-a", "az-b", "az-c"}), "nodes")

	assert.Equal(s.T(), 1, countOSDsInAZRack(nodes, "az-a"))
	assert.Equal(s.T(), 0, countOSDsInAZRack(nodes, "az-nonexistent"))
}

func (s *crushSuite) TestCountOSDsInAZRackEmpty() {
	// az-c rack has a host but no OSDs
	tree := `{"nodes":[
		{"id":-1,"name":"default","type":"root","children":[-2,-3,-4]},
		{"id":-2,"name":"az.az-a","type":"rack","children":[-5]},
		{"id":-5,"name":"host-az-a","type":"host","children":[0]},
		{"id":0,"name":"osd.0","type":"osd"},
		{"id":-3,"name":"az.az-b","type":"rack","children":[-6]},
		{"id":-6,"name":"host-az-b","type":"host","children":[1]},
		{"id":1,"name":"osd.1","type":"osd"},
		{"id":-4,"name":"az.az-c","type":"rack","children":[-7]},
		{"id":-7,"name":"host-az-c","type":"host","children":[]}
	]}`
	nodes := gjson.Get(tree, "nodes")

	assert.Equal(s.T(), 0, countOSDsInAZRack(nodes, "az-c"))
}
