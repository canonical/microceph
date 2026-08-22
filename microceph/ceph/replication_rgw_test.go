package ceph

// Tests for the RGW replication handler. The prefill and status paths run
// against a mocked command runner and the captured JSON in test_assets/; the
// topology and verdict rendering helpers are tested with inline data.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
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
	getRemoteDb func(ctx context.Context, s mcTypes.State, name string) (types.RemoteRecords, error)
}

func TestRgwReplication(t *testing.T) {
	suite.Run(t, new(RgwReplicationSuite))
}

func (s *RgwReplicationSuite) SetupTest() {
	s.BaseSuite.SetupTest()
	s.getRemoteDb = database.GetRemoteDb
}

func (s *RgwReplicationSuite) TearDownTest() {
	database.GetRemoteDb = s.getRemoteDb
}

// setRemotes points the remotes table at the provided records.
func (s *RgwReplicationSuite) setRemotes(records ...types.RemoteRecord) {
	database.GetRemoteDb = func(ctx context.Context, st mcTypes.State, name string) (types.RemoteRecords, error) {
		return records, nil
	}
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

// secondaryHandler is the same topology seen from siteb.
func secondaryHandler() *RgwReplicationHandler {
	rh := masterHandler()
	rh.Zone = RgwZone{ID: siteBZoneID, Name: "siteb"}
	return rh
}

// ############################## PreFill ##############################

func (s *RgwReplicationSuite) TestPreFillMasterZone() {
	r := mocks.NewRunner(s.T())
	s.expectTopologyReads(r, "./test_assets/rgw_zone_get.json", "")

	// A master syncs from no one, so its own metadata markers are never
	// read; only the data markers for its peer zone are.
	dataSync, _ := os.ReadFile("./test_assets/rgw_data_sync_status.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "data", "sync", "status", "--source-zone", "siteb"}...).Return(string(dataSync), nil).Once()
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
	assert.Empty(s.T(), rh.MetadataSync.Info.Status)
	assert.Equal(s.T(), 128, rh.DataSync["siteb"].Info.NumShards)
}

func (s *RgwReplicationSuite) TestPreFillSecondaryZone() {
	r := mocks.NewRunner(s.T())
	s.expectTopologyReads(r, "", siteBZoneGet)

	metaSync, _ := os.ReadFile("./test_assets/rgw_metadata_sync_status_secondary.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "metadata", "sync", "status"}...).Return(string(metaSync), nil).Once()
	dataSync, _ := os.ReadFile("./test_assets/rgw_data_sync_status.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "data", "sync", "status", "--source-zone", "sitea"}...).Return(string(dataSync), nil).Once()
	common.ProcessExec = r

	rh := &RgwReplicationHandler{}
	err := rh.PreFill(context.Background(), types.RgwReplicationRequest{
		RequestType:  types.StatusReplicationRequest,
		ResourceType: types.RgwResourceSite,
	})

	assert.NoError(s.T(), err)
	assert.False(s.T(), rh.isMasterZone())
	assert.Equal(s.T(), 64, rh.MetadataSync.Info.NumShards)
	assert.Contains(s.T(), rh.DataSync, "sitea")
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

// A cluster wide request carries no resource fields at all, and the prefill
// must survive that: it is what every future list and promote arrives as.
func (s *RgwReplicationSuite) TestPreFillToleratesZeroValueRequest() {
	r := mocks.NewRunner(s.T())
	s.expectTopologyReads(r, "./test_assets/rgw_zone_get.json", "")
	common.ProcessExec = r

	rh := &RgwReplicationHandler{}
	err := rh.PreFill(context.Background(), types.RgwReplicationRequest{})

	assert.NoError(s.T(), err)
	// Sync markers are only read for a status request.
	assert.Empty(s.T(), rh.DataSync)
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

func (s *RgwReplicationSuite) TestZoneBriefs() {
	briefs := masterHandler().zoneBriefs()

	assert.Len(s.T(), briefs, 2)
	assert.Equal(s.T(), "siteb", briefs[0].Name)
	assert.False(s.T(), briefs[0].IsMaster)
	assert.False(s.T(), briefs[0].IsLocal)
	assert.Equal(s.T(), "sitea", briefs[1].Name)
	assert.True(s.T(), briefs[1].IsMaster)
	assert.True(s.T(), briefs[1].IsLocal)
}

// ############################## Verdict rendering ##############################

func (s *RgwReplicationSuite) TestSummariseRgwSyncVerdictCaughtUp() {
	brief := summariseRgwSyncVerdict("sitea", "sitea", RgwSyncInfo{Status: "sync", NumShards: 64}, RgwSyncVerdict{CaughtUp: true})

	assert.Equal(s.T(), types.RgwSyncStateCaughtUp, brief.State)
	assert.Equal(s.T(), 64, brief.ShardCount)
	assert.Empty(s.T(), brief.BehindShards)
	assert.Equal(s.T(), 0, brief.FullSyncShards)
}

func (s *RgwReplicationSuite) TestSummariseRgwSyncVerdictBehind() {
	verdict := RgwSyncVerdict{BehindShards: []int{3, 7}, FullSyncShards: 2}
	brief := summariseRgwSyncVerdict("sitea", "sitea", RgwSyncInfo{Status: "sync", NumShards: 64}, verdict)

	assert.Equal(s.T(), types.RgwSyncStateBehind, brief.State)
	assert.Equal(s.T(), []int{3, 7}, brief.BehindShards)
	assert.Equal(s.T(), 2, brief.FullSyncShards)
}

// An unreadable peer is not a claim about how far behind this zone is, and the
// shard counts a short circuited verdict leaves at zero must not travel with
// it: zero behind shards would otherwise read as caught up.
func (s *RgwReplicationSuite) TestSummariseRgwSyncVerdictPeerUnavailable() {
	brief := summariseRgwSyncVerdict("sitea", "", RgwSyncInfo{Status: "sync", NumShards: 64}, RgwSyncVerdict{PeerLogUnavailable: true})

	assert.Equal(s.T(), types.RgwSyncStatePeerUnavailable, brief.State)
	assert.Empty(s.T(), brief.BehindShards)
	assert.Equal(s.T(), 0, brief.FullSyncShards)
}

func (s *RgwReplicationSuite) TestSummariseRgwSyncVerdictPeriodMismatch() {
	verdict := RgwSyncVerdict{PeriodMismatch: true}
	brief := summariseRgwSyncVerdict("sitea", "sitea", RgwSyncInfo{Status: "sync", NumShards: 64}, verdict)

	assert.Equal(s.T(), types.RgwSyncStatePeriodMismatch, brief.State)
}

// ############################## Sync briefs ##############################

func (s *RgwReplicationSuite) TestMetadataSyncBriefOnMaster() {
	brief := masterHandler().metadataSyncBrief(map[string]types.RemoteRecord{})

	assert.Equal(s.T(), types.RgwSyncStateMaster, brief.State)
	assert.Equal(s.T(), "sitea", brief.SourceZone)
}

// Without a remote for the master cluster its metadata log cannot be read, and
// reading the local one instead would compare this zone with itself.
func (s *RgwReplicationSuite) TestMetadataSyncBriefWithoutRemote() {
	rh := secondaryHandler()
	rh.MetadataSync = RgwMetadataSyncStatus{Info: RgwSyncInfo{Status: "sync", NumShards: 64}}

	brief := rh.metadataSyncBrief(map[string]types.RemoteRecord{})

	assert.Equal(s.T(), types.RgwSyncStatePeerUnavailable, brief.State)
	assert.Equal(s.T(), "sitea", brief.SourceZone)
	assert.Empty(s.T(), brief.RemoteName)
}

func (s *RgwReplicationSuite) TestMetadataSyncBriefWithRemote() {
	r := mocks.NewRunner(s.T())
	mdlog, _ := os.ReadFile("./test_assets/rgw_mdlog_status.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "mdlog", "status", "--cluster", "sitea", "--id", "siteb"}...).Return(string(mdlog), nil).Once()
	common.ProcessExec = r

	rh := secondaryHandler()
	rh.MetadataSync = RgwMetadataSyncStatus{
		Info: RgwSyncInfo{Status: "sync", NumShards: 4, Period: "period-1"},
		Markers: []RgwMetadataSyncShard{
			{Key: 0, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental}},
			{Key: 1, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental}},
			{Key: 2, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental}},
			{Key: 3, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental, Marker: "1_1784681399.801225_678.1"}},
		},
	}

	brief := rh.metadataSyncBrief(map[string]types.RemoteRecord{
		"sitea": {Name: "sitea", LocalName: "siteb"},
	})

	// Shard 2's log head is ahead of an empty local marker; shard 3 matches.
	assert.Equal(s.T(), types.RgwSyncStateBehind, brief.State)
	assert.Equal(s.T(), []int{2}, brief.BehindShards)
	assert.Equal(s.T(), "sitea", brief.RemoteName)
}

// A zone left behind on an older period cannot have its markers compared with
// the master's log at all, so it must not read as either caught up or behind.
// This exercises the handler's own wiring of the realm period into the
// comparison, not just the comparison itself.
func (s *RgwReplicationSuite) TestMetadataSyncBriefPeriodMismatch() {
	r := mocks.NewRunner(s.T())
	mdlog, _ := os.ReadFile("./test_assets/rgw_mdlog_status.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "mdlog", "status", "--cluster", "sitea", "--id", "siteb"}...).Return(string(mdlog), nil).Once()
	common.ProcessExec = r

	rh := secondaryHandler()
	rh.MetadataSync = RgwMetadataSyncStatus{
		Info: RgwSyncInfo{Status: "sync", NumShards: 4, Period: "an-older-period"},
		Markers: []RgwMetadataSyncShard{
			{Key: 0, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental}},
		},
	}

	brief := rh.metadataSyncBrief(map[string]types.RemoteRecord{
		"sitea": {Name: "sitea", LocalName: "siteb"},
	})

	assert.Equal(s.T(), types.RgwSyncStatePeriodMismatch, brief.State)
	assert.Empty(s.T(), brief.BehindShards)
	assert.Equal(s.T(), 0, brief.FullSyncShards)
}

func (s *RgwReplicationSuite) TestDataSyncBriefsWithoutRemote() {
	rh := masterHandler()
	rh.DataSync = map[string]RgwDataSyncStatus{
		"siteb": {Info: RgwSyncInfo{Status: "sync", NumShards: 128}},
	}

	briefs := rh.dataSyncBriefs(map[string]types.RemoteRecord{})

	assert.Len(s.T(), briefs, 1)
	assert.Equal(s.T(), "siteb", briefs[0].SourceZone)
	assert.Equal(s.T(), types.RgwSyncStatePeerUnavailable, briefs[0].State)
}

func (s *RgwReplicationSuite) TestDataSyncBriefsWithRemote() {
	r := mocks.NewRunner(s.T())
	datalog, _ := os.ReadFile("./test_assets/rgw_datalog_status.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "datalog", "status", "--cluster", "siteb", "--id", "sitea"}...).Return(string(datalog), nil).Once()
	common.ProcessExec = r

	rh := masterHandler()
	rh.DataSync = map[string]RgwDataSyncStatus{
		"siteb": {
			Info: RgwSyncInfo{Status: "sync", NumShards: 3},
			Markers: []RgwDataSyncShard{
				{Key: 0, Val: RgwDataSyncMarker{Status: "incremental-sync"}},
				{Key: 1, Val: RgwDataSyncMarker{Status: "incremental-sync"}},
				{Key: 2, Val: RgwDataSyncMarker{Status: "incremental-sync", Marker: "00000000000000000000:00000000000000000512"}},
			},
		},
	}

	briefs := rh.dataSyncBriefs(map[string]types.RemoteRecord{
		"siteb": {Name: "siteb", LocalName: "sitea"},
	})

	assert.Len(s.T(), briefs, 1)
	assert.Equal(s.T(), types.RgwSyncStateCaughtUp, briefs[0].State)
	assert.Equal(s.T(), "siteb", briefs[0].RemoteName)
}

// ############################## StatusHandler ##############################

func (s *RgwReplicationSuite) TestStatusHandler() {
	r := mocks.NewRunner(s.T())
	datalog, _ := os.ReadFile("./test_assets/rgw_datalog_status.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "datalog", "status", "--cluster", "siteb", "--id", "sitea"}...).Return(string(datalog), nil).Once()
	common.ProcessExec = r
	s.setRemotes(types.RemoteRecord{Name: "siteb", LocalName: "sitea"})

	rh := masterHandler()
	rh.Request = types.RgwReplicationRequest{
		RequestType:  types.StatusReplicationRequest,
		ResourceType: types.RgwResourceSite,
	}
	rh.DataSync = map[string]RgwDataSyncStatus{
		"siteb": {Info: RgwSyncInfo{Status: "sync", NumShards: 3}},
	}

	var resp string
	err := rh.StatusHandler(context.Background(), rh, &resp, interfaces.CephState{})
	assert.NoError(s.T(), err)

	var status types.RgwReplicationResponseStatus
	err = json.Unmarshal([]byte(resp), &status)
	assert.NoError(s.T(), err)

	assert.Equal(s.T(), "microceph", status.Realm)
	assert.Equal(s.T(), 2, status.RealmEpoch)
	assert.Equal(s.T(), "sitea", status.Zone)
	assert.True(s.T(), status.IsMasterZone)
	assert.Equal(s.T(), "sitea", status.MasterZone)
	assert.Len(s.T(), status.Zones, 2)
	assert.Equal(s.T(), types.RgwSyncStateMaster, status.MetadataSync.State)
	assert.Len(s.T(), status.DataSync, 1)
	assert.Equal(s.T(), "siteb", status.DataSync[0].SourceZone)
}

// Bucket scoped status is a later rung of the ladder; until then it says so
// rather than silently answering with the site wide view.
func (s *RgwReplicationSuite) TestStatusHandlerRejectsBucketScope() {
	rh := masterHandler()
	rh.Request = types.RgwReplicationRequest{
		RequestType:  types.StatusReplicationRequest,
		ResourceType: types.RgwResourceBucket,
		Bucket:       "photos",
	}

	var resp string
	err := rh.StatusHandler(context.Background(), rh, &resp, interfaces.CephState{})
	assert.ErrorContains(s.T(), err, "not implemented for rgw")
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
}

// The handler must be reachable through the workload registry, or the API
// answers every rgw request with "no replication handler".
func (s *RgwReplicationSuite) TestHandlerIsRegistered() {
	rh := GetReplicationHandler(string(types.RgwWorkload))
	assert.NotNil(s.T(), rh)
	assert.IsType(s.T(), &RgwReplicationHandler{}, rh)
}
