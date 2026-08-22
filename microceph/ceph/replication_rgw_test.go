package ceph

// Tests for the RGW replication handler. The prefill path runs against a
// mocked command runner and the captured JSON in test_assets/; the topology
// helpers are tested with inline data.

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
)

const (
	siteAZoneID = "7b7a8b32-3e1e-4bab-9965-e756fbe29aa7"
	siteBZoneID = "58a9f4ec-c0b7-415d-93a5-8eb1c03818ae"
)

// siteBZoneGet is a `zone get` response trimmed to the fields the handler
// reads, standing in for the secondary side of the captured two site pair.
const siteBZoneGet = `{"id": "58a9f4ec-c0b7-415d-93a5-8eb1c03818ae", "name": "siteb"}`

type RgwReplicationSuite struct {
	tests.BaseSuite
}

func TestRgwReplication(t *testing.T) {
	suite.Run(t, new(RgwReplicationSuite))
}

func (s *RgwReplicationSuite) SetupTest() {
	s.BaseSuite.SetupTest()
}

// masterHandler is the local cluster as the captured fixtures describe it:
// zone sitea, which is the zonegroup's master, with siteb as its peer.
func masterHandler() *RgwReplicationHandler {
	return &RgwReplicationHandler{
		Realm: RgwRealm{Name: "microceph", CurrentPeriod: "period-1", Epoch: 2},
		ZoneGroup: RgwZoneGroup{
			Name:       "microceph",
			MasterZone: siteAZoneID,
			Zones: []RgwZoneGroupZone{
				{ID: siteBZoneID, Name: "siteb", Endpoints: []string{"http://10.85.32.128:80"}},
				{ID: siteAZoneID, Name: "sitea", Endpoints: []string{"http://10.85.32.250:80"}},
			},
		},
		Zone: RgwZone{ID: siteAZoneID, Name: "sitea"},
	}
}

// ############################## PreFill ##############################

func (s *RgwReplicationSuite) TestPreFillMasterZone() {
	r := mocks.NewRunner(s.T())
	s.expectTopologyReads(r, "./test_assets/rgw_zone_get.json", "")
	common.ProcessExec = r

	rh := &RgwReplicationHandler{}
	err := rh.PreFill(context.Background(), types.RgwReplicationRequest{
		RequestType:  types.StatusReplicationRequest,
		ResourceType: types.RgwResourceSite,
	})

	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "microceph", rh.Realm.Name)
	assert.Equal(s.T(), "sitea", rh.Zone.Name)
	assert.True(s.T(), rh.isMasterZone())
}

func (s *RgwReplicationSuite) TestPreFillSecondaryZone() {
	r := mocks.NewRunner(s.T())
	s.expectTopologyReads(r, "", siteBZoneGet)
	common.ProcessExec = r

	rh := &RgwReplicationHandler{}
	err := rh.PreFill(context.Background(), types.RgwReplicationRequest{
		RequestType:  types.StatusReplicationRequest,
		ResourceType: types.RgwResourceSite,
	})

	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "siteb", rh.Zone.Name)
	assert.False(s.T(), rh.isMasterZone())
}

// A gateway with no realm stops the prefill dead: there is no topology to
// read, and every further call would come back empty anyway.
func (s *RgwReplicationSuite) TestPreFillUnconfigured() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "realm", "get"}...).Return("", fmt.Errorf("failed to load realm: (2) No such file or directory")).Once()
	common.ProcessExec = r

	rh := &RgwReplicationHandler{}
	err := rh.PreFill(context.Background(), types.RgwReplicationRequest{
		RequestType:  types.StatusReplicationRequest,
		ResourceType: types.RgwResourceSite,
	})

	assert.NoError(s.T(), err)
	assert.Empty(s.T(), rh.Realm.Name)

	state, err := rh.GetResourceState()
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), StateDisabledReplication, state)
}

// expectTopologyReads queues the realm, zonegroup and zone reads every prefill
// starts with. Pass either a zone fixture path or an inline zone response.
func (s *RgwReplicationSuite) expectTopologyReads(r *mocks.Runner, zoneFixture string, zoneResponse string) {
	realm, _ := os.ReadFile("./test_assets/rgw_realm_get.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "realm", "get"}...).Return(string(realm), nil).Once()

	zonegroup, _ := os.ReadFile("./test_assets/rgw_zonegroup_get.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "zonegroup", "get"}...).Return(string(zonegroup), nil).Once()

	if len(zoneFixture) != 0 {
		zone, _ := os.ReadFile(zoneFixture)
		zoneResponse = string(zone)
	}
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "zone", "get"}...).Return(zoneResponse, nil).Once()
}

// ############################## GetResourceState ##############################

func (s *RgwReplicationSuite) TestGetResourceStateEnabled() {
	state, err := masterHandler().GetResourceState()
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), StateEnabledReplication, state)
}

// A zone removed from the zonegroup keeps its realm and its own configuration,
// but nothing replicates to or from it any more.
func (s *RgwReplicationSuite) TestGetResourceStateZoneNotInZoneGroup() {
	rh := masterHandler()
	rh.Zone = RgwZone{ID: "orphaned-zone-id", Name: "sitec"}

	state, err := rh.GetResourceState()
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), StateDisabledReplication, state)
}

func (s *RgwReplicationSuite) TestGetResourceStateNoZone() {
	rh := masterHandler()
	rh.Zone = RgwZone{}

	state, err := rh.GetResourceState()
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), StateDisabledReplication, state)
}

// ############################## Topology helpers ##############################

func (s *RgwReplicationSuite) TestTopologyHelpers() {
	rh := masterHandler()

	assert.True(s.T(), rh.isMasterZone())
	assert.True(s.T(), rh.isZoneGroupMember())
	assert.Equal(s.T(), "sitea", rh.masterZoneName())

	peers := rh.peerZones()
	assert.Len(s.T(), peers, 1)
	assert.Equal(s.T(), "siteb", peers[0].Name)
}

// ############################## Unimplemented verbs ##############################

func (s *RgwReplicationSuite) TestUnimplementedVerbs() {
	rh := masterHandler()
	ctx := context.Background()

	assert.ErrorContains(s.T(), rh.EnableHandler(ctx), "not implemented for rgw")
	assert.ErrorContains(s.T(), rh.DisableHandler(ctx), "not implemented for rgw")
	assert.ErrorContains(s.T(), rh.ConfigureHandler(ctx), "not implemented for rgw")
	assert.ErrorContains(s.T(), rh.ListHandler(ctx), "not implemented for rgw")
	assert.ErrorContains(s.T(), rh.PromoteHandler(ctx), "not implemented for rgw")
	assert.ErrorContains(s.T(), rh.DemoteHandler(ctx), "not implemented for rgw")
	assert.ErrorContains(s.T(), rh.StatusHandler(ctx), "not implemented for rgw")
}

// The handler must be reachable through the workload registry, or the API
// answers every rgw request with "no replication handler".
func (s *RgwReplicationSuite) TestHandlerIsRegistered() {
	rh := GetReplicationHandler(string(types.RgwWorkload))
	assert.NotNil(s.T(), rh)
	assert.IsType(s.T(), &RgwReplicationHandler{}, rh)
}
