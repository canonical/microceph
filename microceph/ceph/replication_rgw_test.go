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
	siteCZoneID = "9c151f92-d92b-4c28-a5a1-4f0f4dd2ea11"

	microcephZoneGroupID = "67be86c9-2912-4ce2-835d-9bdf91915363"
	euZoneGroupID        = "d6d1a03a-40c5-4f4a-9ce6-3b4c2f04a1de"
)

// siteBZoneGet is a `zone get` response trimmed to the fields the handler
// reads, standing in for the secondary side of the captured two site pair.
const siteBZoneGet = `{"id": "58a9f4ec-c0b7-415d-93a5-8eb1c03818ae", "name": "siteb"}`

// siteCZoneGet is the local zone of a second, non-master zonegroup in the
// same realm the captured fixtures describe.
const siteCZoneGet = `{"id": "9c151f92-d92b-4c28-a5a1-4f0f4dd2ea11", "name": "sitec"}`

// euZoneGroupGet is a `zonegroup get` response for that second zonegroup:
// not the realm's master, with sitec as its own master and only member.
const euZoneGroupGet = `{
	"id": "d6d1a03a-40c5-4f4a-9ce6-3b4c2f04a1de",
	"name": "eu",
	"is_master": false,
	"master_zone": "9c151f92-d92b-4c28-a5a1-4f0f4dd2ea11",
	"zones": [{"id": "9c151f92-d92b-4c28-a5a1-4f0f4dd2ea11", "name": "sitec", "endpoints": ["http://10.85.33.10:80"]}],
	"realm_id": "cf90947b-b444-488d-abd3-779c3c6062d7"
}`

// euPeriodGet is the realm period as sitec sees it: both zonegroups, with
// the master zonegroup being the captured microceph one holding sitea.
const euPeriodGet = `{
	"master_zonegroup": "67be86c9-2912-4ce2-835d-9bdf91915363",
	"master_zone": "7b7a8b32-3e1e-4bab-9965-e756fbe29aa7",
	"period_map": {"zonegroups": [
		{
			"id": "67be86c9-2912-4ce2-835d-9bdf91915363",
			"name": "microceph",
			"is_master": true,
			"master_zone": "7b7a8b32-3e1e-4bab-9965-e756fbe29aa7",
			"zones": [
				{"id": "7b7a8b32-3e1e-4bab-9965-e756fbe29aa7", "name": "sitea"},
				{"id": "58a9f4ec-c0b7-415d-93a5-8eb1c03818ae", "name": "siteb"}
			]
		},
		{
			"id": "d6d1a03a-40c5-4f4a-9ce6-3b4c2f04a1de",
			"name": "eu",
			"is_master": false,
			"master_zone": "9c151f92-d92b-4c28-a5a1-4f0f4dd2ea11",
			"zones": [{"id": "9c151f92-d92b-4c28-a5a1-4f0f4dd2ea11", "name": "sitec"}]
		}
	]}
}`

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
			IsMaster:   true,
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

// A local metadata sync status command that cannot run is a per stream
// outage: the rest of the prefill must survive it and the failure must be
// recorded rather than left as a convincing zero value.
func (s *RgwReplicationSuite) TestPreFillMetadataSyncUnavailable() {
	r := mocks.NewRunner(s.T())
	s.expectTopologyReads(r, "", siteBZoneGet)
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "metadata", "sync", "status"}...).Return("", fmt.Errorf("exit status 5")).Once()
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
	assert.True(s.T(), rh.MetadataSyncUnavailable)
	assert.Empty(s.T(), rh.MetadataSync.Info.Status)
	assert.Equal(s.T(), 128, rh.DataSync["sitea"].Info.NumShards)
}

// The same outage on one data stream must leave the metadata stream and the
// map bookkeeping intact.
func (s *RgwReplicationSuite) TestPreFillDataSyncUnavailable() {
	r := mocks.NewRunner(s.T())
	s.expectTopologyReads(r, "", siteBZoneGet)
	metaSync, _ := os.ReadFile("./test_assets/rgw_metadata_sync_status_secondary.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "metadata", "sync", "status"}...).Return(string(metaSync), nil).Once()
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "data", "sync", "status", "--source-zone", "sitea"}...).Return("", fmt.Errorf("exit status 5")).Once()
	common.ProcessExec = r

	rh := &RgwReplicationHandler{}
	err := rh.PreFill(context.Background(), types.RgwReplicationRequest{
		RequestType:  types.StatusReplicationRequest,
		ResourceType: types.RgwResourceSite,
	})

	assert.NoError(s.T(), err)
	assert.True(s.T(), rh.DataSyncUnavailable["sitea"])
	assert.NotContains(s.T(), rh.DataSync, "sitea")
	assert.Equal(s.T(), 64, rh.MetadataSync.Info.NumShards)
}

// A gateway that answers with a self-contradictory sync status is corrupt
// data rather than an outage, and must keep failing the whole request.
func (s *RgwReplicationSuite) TestPreFillMalformedSyncStatusStillFails() {
	r := mocks.NewRunner(s.T())
	s.expectTopologyReads(r, "", siteBZoneGet)
	invalid := `{"sync_status":{"info":{"status":"sync","num_shards":2},"markers":[{"key":5,"val":{"state":1,"marker":""}}]}}`
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "metadata", "sync", "status"}...).Return(invalid, nil).Once()
	common.ProcessExec = r

	rh := &RgwReplicationHandler{}
	err := rh.PreFill(context.Background(), types.RgwReplicationRequest{
		RequestType:  types.StatusReplicationRequest,
		ResourceType: types.RgwResourceSite,
	})

	assert.Error(s.T(), err)
}

// A zone that is master of its own, non-master zonegroup is the exact
// topology the realm-wide master check exists for: it must still read its
// own metadata sync markers, and the metadata master it names lives in a
// zonegroup only the realm period can see.
func (s *RgwReplicationSuite) TestPreFillMasterOfNonMasterZoneGroup() {
	r := mocks.NewRunner(s.T())
	realm, _ := os.ReadFile("./test_assets/rgw_realm_get.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "realm", "get"}...).Return(string(realm), nil).Once()
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "zonegroup", "get"}...).Return(euZoneGroupGet, nil).Once()
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "zone", "get"}...).Return(siteCZoneGet, nil).Once()
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "period", "get"}...).Return(euPeriodGet, nil).Once()
	metaSync, _ := os.ReadFile("./test_assets/rgw_metadata_sync_status_secondary.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "metadata", "sync", "status"}...).Return(string(metaSync), nil).Once()
	common.ProcessExec = r

	rh := &RgwReplicationHandler{}
	err := rh.PreFill(context.Background(), types.RgwReplicationRequest{
		RequestType:  types.StatusReplicationRequest,
		ResourceType: types.RgwResourceSite,
	})

	assert.NoError(s.T(), err)
	assert.False(s.T(), rh.isMasterZone())
	assert.Equal(s.T(), 64, rh.MetadataSync.Info.NumShards)
	assert.Equal(s.T(), "sitea", rh.masterZoneName())
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

// A zone can be the master of its own zonegroup while the realm's metadata
// master lives in a different zonegroup. Such a zone still syncs metadata
// and must not present itself as a master.
func (s *RgwReplicationSuite) TestIsMasterZoneOfNonMasterZoneGroup() {
	rh := masterHandler()
	rh.ZoneGroup.IsMaster = false

	assert.False(s.T(), rh.isMasterZone())
}

// In a non-master zonegroup the metadata master's name comes from the realm
// period, since the local zonegroup listing cannot contain it.
func (s *RgwReplicationSuite) TestMasterZoneNameAcrossZoneGroups() {
	rh := masterHandler()
	rh.ZoneGroup.IsMaster = false
	rh.Period = RgwPeriod{
		MasterZonegroup: euZoneGroupID,
		MasterZone:      siteCZoneID,
		PeriodMap: RgwPeriodMap{
			ZoneGroups: []RgwZoneGroup{
				rh.ZoneGroup,
				{
					ID:         euZoneGroupID,
					Name:       "eu",
					IsMaster:   true,
					MasterZone: siteCZoneID,
					Zones:      []RgwZoneGroupZone{{ID: siteCZoneID, Name: "sitec"}},
				},
			},
		},
	}

	assert.Equal(s.T(), "sitec", rh.masterZoneName())
}

// Without a period the cross-zonegroup master cannot be named at all, which
// must read as empty rather than as the local zonegroup's own master.
func (s *RgwReplicationSuite) TestMasterZoneNameNonMasterZoneGroupNoPeriod() {
	rh := masterHandler()
	rh.ZoneGroup.IsMaster = false

	assert.Empty(s.T(), rh.masterZoneName())
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

// A local status that was never read carries no shard counts or sync state
// worth showing, and must not fall through to a real verdict.
func (s *RgwReplicationSuite) TestSummariseRgwSyncVerdictLocalUnavailable() {
	brief := summariseRgwSyncVerdict("sitea", "sitea", RgwSyncInfo{}, RgwSyncVerdict{LocalUnavailable: true})

	assert.Equal(s.T(), types.RgwSyncStateLocalUnavailable, brief.State)
	assert.Empty(s.T(), brief.SyncStatus)
	assert.Equal(s.T(), 0, brief.ShardCount)
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

// A failed local metadata read renders as local-unavailable without ever
// touching the peer: no command may run here.
func (s *RgwReplicationSuite) TestMetadataSyncBriefLocalUnavailable() {
	common.ProcessExec = mocks.NewRunner(s.T())

	rh := secondaryHandler()
	rh.MetadataSyncUnavailable = true

	brief := rh.metadataSyncBrief(map[string]types.RemoteRecord{
		"sitea": {Name: "sitea", LocalName: "siteb"},
	})

	assert.Equal(s.T(), types.RgwSyncStateLocalUnavailable, brief.State)
	assert.Equal(s.T(), "sitea", brief.SourceZone)
	assert.Equal(s.T(), "sitea", brief.RemoteName)
	assert.Empty(s.T(), brief.SyncStatus)
	assert.Equal(s.T(), 0, brief.ShardCount)
}

// The same rendering per data stream.
func (s *RgwReplicationSuite) TestDataSyncBriefsLocalUnavailable() {
	common.ProcessExec = mocks.NewRunner(s.T())

	rh := masterHandler()
	rh.DataSync = map[string]RgwDataSyncStatus{}
	rh.DataSyncUnavailable = map[string]bool{"siteb": true}

	briefs := rh.dataSyncBriefs(map[string]types.RemoteRecord{
		"siteb": {Name: "siteb", LocalName: "sitea"},
	})

	assert.Len(s.T(), briefs, 1)
	assert.Equal(s.T(), types.RgwSyncStateLocalUnavailable, briefs[0].State)
	assert.Equal(s.T(), "siteb", briefs[0].SourceZone)
	assert.Equal(s.T(), "siteb", briefs[0].RemoteName)
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
