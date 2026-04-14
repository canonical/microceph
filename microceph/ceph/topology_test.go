package ceph

import (
	"context"
	"fmt"
	"testing"

	"github.com/canonical/lxd/shared/api"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

type topologySuite struct {
	tests.BaseSuite
}

func TestTopology(t *testing.T) {
	suite.Run(t, new(topologySuite))
}

// withMockAZData swaps the package-level getAZData for a mock that returns
// the given azData, returning a restore function.
func withMockAZData(data azData) func() {
	orig := getAZData
	getAZData = func(_ context.Context, _ mcTypes.State, _ string) (azData, error) {
		return data, nil
	}
	return func() { getAZData = orig }
}

// addCrushBucketExpectations sets up mock expectations for the 4 CRUSH
// bucket commands issued by updateTopology (via cephRunContext → RunCommandContext).
// The az parameter is the user-facing AZ name; the CRUSH bucket uses the "az." prefix.
func addCrushBucketExpectations(r *mocks.Runner, az string, hostname string) {
	rackBucket := fmt.Sprintf("az.%s", az)
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "crush", "add-bucket", rackBucket, "rack").Return("", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "crush", "move", rackBucket, "root=default").Return("", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "crush", "add-bucket", hostname, "host").Return("", nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "crush", "move", hostname, fmt.Sprintf("rack=%s", rackBucket)).Return("", nil).Once()
}

// TestUpdateTopologyNoAZ verifies that updateTopology is a no-op when no AZ
// is configured for the host.
func (s *topologySuite) TestUpdateTopologyNoAZ() {
	defer withMockAZData(azData{})()

	u := api.NewURL()
	state := &mocks.MockState{URL: u, ClusterName: "node1"}
	si := mocks.NewStateInterface(s.T())
	si.On("ClusterState").Return(state).Maybe()

	// No runner expectations — nothing should be called.
	r := mocks.NewRunner(s.T())
	common.ProcessExec = r

	mgr := NewOSDManager(si.ClusterState())
	mgr.fs = afero.NewMemMapFs()

	err := mgr.updateTopology(context.Background())
	assert.NoError(s.T(), err)
}

// TestUpdateTopologyAZLessThan3 verifies that with an AZ configured but fewer
// than 3 unique AZs, CRUSH bucket commands are run but no rule switch happens.
func (s *topologySuite) TestUpdateTopologyAZLessThan3() {
	defer withMockAZData(azData{
		hostAZ: "az-1",
		uniqueAZs: map[string]bool{
			"az-1": true,
			"az-2": true,
		},
	})()

	u := api.NewURL()
	state := &mocks.MockState{URL: u, ClusterName: "node1"}
	si := mocks.NewStateInterface(s.T())
	si.On("ClusterState").Return(state).Maybe()

	r := mocks.NewRunner(s.T())
	addCrushBucketExpectations(r, "az-1", "node1")
	common.ProcessExec = r

	mgr := NewOSDManager(si.ClusterState())
	mgr.fs = afero.NewMemMapFs()

	err := mgr.updateTopology(context.Background())
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

// osdTreeWithOSDs returns a CRUSH tree JSON where each of the given AZs
// contains a host with one OSD. Rack bucket names use the "az." prefix.
func osdTreeWithOSDs(azs []string) string {
	// root node children: one rack per AZ (IDs -2, -3, ...)
	nodes := `{"nodes":[{"id":-1,"name":"default","type":"root","children":[`
	for i := range azs {
		if i > 0 {
			nodes += ","
		}
		nodes += fmt.Sprintf("%d", -(i + 2))
	}
	nodes += "]}"
	for i, az := range azs {
		rackID := -(i + 2)
		hostID := -(i + 2 + len(azs))
		osdID := i
		nodes += fmt.Sprintf(
			`,{"id":%d,"name":"az.%s","type":"rack","children":[%d]}`+
				`,{"id":%d,"name":"host-%s","type":"host","children":[%d]}`+
				`,{"id":%d,"name":"osd.%d","type":"osd"}`,
			rackID, az, hostID,
			hostID, az, osdID,
			osdID, osdID,
		)
	}
	nodes += "]}"
	return nodes
}

// osdTreeEmpty returns a CRUSH tree JSON with no nodes.
func osdTreeEmpty() string {
	return `{"nodes":[{"id":-1,"name":"default","type":"root","children":[]}]}`
}

// TestUpdateTopologyAZ3OrMore verifies that with 3+ unique AZs each having
// an OSD, CRUSH bucket commands are run AND the failure domain is switched to rack.
func (s *topologySuite) TestUpdateTopologyAZ3OrMore() {
	defer withMockAZData(azData{
		hostAZ: "az-1",
		uniqueAZs: map[string]bool{
			"az-1": true,
			"az-2": true,
			"az-3": true,
		},
	})()

	u := api.NewURL()
	state := &mocks.MockState{URL: u, ClusterName: "node1"}
	si := mocks.NewStateInterface(s.T())
	si.On("ClusterState").Return(state).Maybe()

	r := mocks.NewRunner(s.T())

	// CRUSH bucket setup commands.
	addCrushBucketExpectations(r, "az-1", "node1")

	// osd tree for countAZsWithOSDs — all 3 AZs have OSDs.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "tree", "-f", "json").Return(
		osdTreeWithOSDs([]string{"az-1", "az-2", "az-3"}), nil).Maybe()

	// haveCrushRule + switchFailureDomain internals are tested separately
	// by TestSwitchHostFailureDomain. Here we just stub the ceph commands.
	// crush rule dump (6 args).
	r.On("RunCommand", "ceph", "osd", "crush", "rule", "dump", mock.Anything).Return(`{ "rule_id": 3 }`, nil).Maybe()
	// pool ls detail (6 args).
	r.On("RunCommand", "ceph", "osd", "pool", "ls", "detail", "--format=json").Return("[]", nil).Maybe()
	// crush rule ls (5 args).
	r.On("RunCommand", "ceph", "osd", "crush", "rule", "ls").Return("microceph_auto_osd\nmicroceph_auto_host\nmicroceph_auto_rack", nil).Maybe()
	// setPoolCrushRule (7 args) and setConfigItem (8 args).
	r.On("RunCommand", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", nil).Maybe()
	r.On("RunCommand", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", nil).Maybe()

	common.ProcessExec = r

	mgr := NewOSDManager(si.ClusterState())
	mgr.fs = afero.NewMemMapFs()

	err := mgr.updateTopology(context.Background())
	assert.NoError(s.T(), err)
}

// TestUpdateTopologyAZ3NoOSDs verifies that with 3+ unique AZs but no OSDs
// in the CRUSH tree, CRUSH bucket commands are run but no rule switch happens.
func (s *topologySuite) TestUpdateTopologyAZ3NoOSDs() {
	defer withMockAZData(azData{
		hostAZ: "az-1",
		uniqueAZs: map[string]bool{
			"az-1": true,
			"az-2": true,
			"az-3": true,
		},
	})()

	u := api.NewURL()
	state := &mocks.MockState{URL: u, ClusterName: "node1"}
	si := mocks.NewStateInterface(s.T())
	si.On("ClusterState").Return(state).Maybe()

	r := mocks.NewRunner(s.T())

	// CRUSH bucket setup commands.
	addCrushBucketExpectations(r, "az-1", "node1")

	// osd tree for countAZsWithOSDs — no AZs have OSDs.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "tree", "-f", "json").Return(osdTreeEmpty(), nil).Once()

	// No switchFailureDomain expectations — should not be called.
	common.ProcessExec = r

	mgr := NewOSDManager(si.ClusterState())
	mgr.fs = afero.NewMemMapFs()

	err := mgr.updateTopology(context.Background())
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

// TestUpdateFailureDomainSkipsWithAZs verifies that updateFailureDomain
// returns early when AZs are configured.
func (s *topologySuite) TestUpdateFailureDomainSkipsWithAZs() {
	defer withMockAZData(azData{
		hostAZ: "az-1",
		uniqueAZs: map[string]bool{
			"az-1": true,
		},
	})()

	u := api.NewURL()
	state := &mocks.MockState{URL: u, ClusterName: "node1"}
	si := mocks.NewStateInterface(s.T())
	si.On("ClusterState").Return(state).Maybe()

	// No runner expectations — switchFailureDomain should NOT be called.
	r := mocks.NewRunner(s.T())
	common.ProcessExec = r

	mgr := NewOSDManager(si.ClusterState())
	mgr.fs = afero.NewMemMapFs()

	c := mocks.NewMemberCounterInterface(s.T())
	database.MemberCounter = c
	// Count should NOT be called since we return early.

	err := mgr.updateFailureDomain(context.Background(), si.ClusterState())
	assert.NoError(s.T(), err)
}

// TestUpdateFailureDomainNoAZs verifies that updateFailureDomain works
// normally (switches to host at 3+ nodes) when no AZs are configured.
func (s *topologySuite) TestUpdateFailureDomainNoAZs() {
	defer withMockAZData(azData{})()

	u := api.NewURL()
	state := &mocks.MockState{URL: u, ClusterName: "node1"}
	si := mocks.NewStateInterface(s.T())
	si.On("ClusterState").Return(state).Maybe()

	r := mocks.NewRunner(s.T())

	// switchFailureDomain("osd", "host") expectations.
	addCrushRuleDumpExpectations(r)
	addSetDefaultRuleExpectations(r)
	addCrushRuleLsExpectations(r)
	addCrushRuleDumpExpectations(r)
	addCrushRuleLsJsonExpectations(r)
	addOsdPoolSetExpectations(r)

	common.ProcessExec = r

	c := mocks.NewMemberCounterInterface(s.T())
	c.On("Count", mock.Anything, mock.Anything).Return(3, nil).Once()
	database.MemberCounter = c

	mgr := NewOSDManager(si.ClusterState())
	mgr.fs = afero.NewMemMapFs()

	err := mgr.updateFailureDomain(context.Background(), si.ClusterState())
	assert.NoError(s.T(), err)
}
