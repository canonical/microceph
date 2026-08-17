package ceph

// Tests for the RGW multisite read wrappers. The radosgw-admin wrappers
// run against a mocked command runner and captured JSON in test_assets/;
// the pure verdict and validation helpers are tested with inline data.

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
)

type RgwMultisiteSuite struct {
	tests.BaseSuite
}

func TestRgwMultisite(t *testing.T) {
	suite.Run(t, new(RgwMultisiteSuite))
}

func (s *RgwMultisiteSuite) SetupTest() {
	s.BaseSuite.SetupTest()
}

func (s *RgwMultisiteSuite) TestGetRgwRealm() {
	r := mocks.NewRunner(s.T())

	output, _ := os.ReadFile("./test_assets/rgw_realm_get.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "realm", "get"}...).Return(string(output), nil).Once()
	common.ProcessExec = r

	realm, err := GetRgwRealm("", "")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "microceph", realm.Name)
	assert.Equal(s.T(), "cf90947b-b444-488d-abd3-779c3c6062d7", realm.ID)
	assert.Equal(s.T(), "9b9a5a4f-ecb4-42a1-b2ff-b31fc0ef5b1b", realm.CurrentPeriod)
	assert.Equal(s.T(), 2, realm.Epoch)
}

func (s *RgwMultisiteSuite) TestGetRgwRealmRemote() {
	r := mocks.NewRunner(s.T())

	output, _ := os.ReadFile("./test_assets/rgw_realm_get.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "realm", "get", "--cluster", "siteb", "--id", "sitea"}...).Return(string(output), nil).Once()
	common.ProcessExec = r

	realm, err := GetRgwRealm("siteb", "sitea")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "microceph", realm.Name)
}

func (s *RgwMultisiteSuite) TestGetRgwRealmUnconfigured() {
	r := mocks.NewRunner(s.T())

	// A realm-less gateway fails realm get; the wrapper swallows the exec
	// error into a zero-value realm (the RBD wrapper contract).
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "realm", "get"}...).Return("", fmt.Errorf("failed to load realm: (2) No such file or directory")).Once()
	common.ProcessExec = r

	realm, err := GetRgwRealm("", "")
	assert.NoError(s.T(), err)
	assert.Empty(s.T(), realm.ID)
	assert.Empty(s.T(), realm.Name)
}

func (s *RgwMultisiteSuite) TestGetRgwZoneGroup() {
	r := mocks.NewRunner(s.T())

	output, _ := os.ReadFile("./test_assets/rgw_zonegroup_get.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "zonegroup", "get"}...).Return(string(output), nil).Once()
	common.ProcessExec = r

	zonegroup, err := GetRgwZoneGroup("", "")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "microceph", zonegroup.Name)
	assert.True(s.T(), zonegroup.IsMaster)
	assert.Equal(s.T(), "7b7a8b32-3e1e-4bab-9965-e756fbe29aa7", zonegroup.MasterZone)
	assert.Len(s.T(), zonegroup.Zones, 2)

	names := []string{zonegroup.Zones[0].Name, zonegroup.Zones[1].Name}
	assert.Contains(s.T(), names, "sitea")
	assert.Contains(s.T(), names, "siteb")
	assert.NotEmpty(s.T(), zonegroup.Zones[0].Endpoints)
}

func (s *RgwMultisiteSuite) TestGetRgwZone() {
	r := mocks.NewRunner(s.T())

	output, _ := os.ReadFile("./test_assets/rgw_zone_get.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "zone", "get"}...).Return(string(output), nil).Once()
	common.ProcessExec = r

	zone, err := GetRgwZone("", "")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "sitea", zone.Name)
	assert.Equal(s.T(), "7b7a8b32-3e1e-4bab-9965-e756fbe29aa7", zone.ID)
	assert.NotEmpty(s.T(), zone.SystemKey.AccessKey)
	assert.NotEmpty(s.T(), zone.SystemKey.SecretKey)
}

func (s *RgwMultisiteSuite) TestGetRgwMetadataSyncStatusSecondary() {
	r := mocks.NewRunner(s.T())

	output, _ := os.ReadFile("./test_assets/rgw_metadata_sync_status_secondary.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "metadata", "sync", "status", "--cluster", "siteb", "--id", "sitea"}...).Return(string(output), nil).Once()
	common.ProcessExec = r

	status, err := GetRgwMetadataSyncStatus("siteb", "sitea")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "sync", status.Info.Status)
	assert.Equal(s.T(), 64, status.Info.NumShards)
	assert.NotEmpty(s.T(), status.Info.Period)
	assert.Equal(s.T(), 2, status.Info.RealmEpoch)
	assert.NotEmpty(s.T(), status.Markers)
	assert.Equal(s.T(), RgwMetadataSyncStateIncremental, status.Markers[0].Val.State)
}

func (s *RgwMultisiteSuite) TestGetRgwMetadataSyncStatusInvalidResponse() {
	r := mocks.NewRunner(s.T())

	// num_shards says 2 but the only marker claims shard 5.
	output := `{"sync_status":{"info":{"status":"sync","num_shards":2},"markers":[{"key":5,"val":{"state":1,"marker":""}}]}}`
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "metadata", "sync", "status"}...).Return(output, nil).Once()
	common.ProcessExec = r

	_, err := GetRgwMetadataSyncStatus("", "")
	assert.Error(s.T(), err)
}

func (s *RgwMultisiteSuite) TestGetRgwMetadataSyncStatusMaster() {
	r := mocks.NewRunner(s.T())

	// The metadata master runs no metadata sync: status "init", no shards.
	output, _ := os.ReadFile("./test_assets/rgw_metadata_sync_status_master.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "metadata", "sync", "status"}...).Return(string(output), nil).Once()
	common.ProcessExec = r

	status, err := GetRgwMetadataSyncStatus("", "")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "init", status.Info.Status)
	assert.Equal(s.T(), 0, status.Info.NumShards)
	assert.Empty(s.T(), status.Markers)
}

func (s *RgwMultisiteSuite) TestGetRgwDataSyncStatus() {
	r := mocks.NewRunner(s.T())

	output, _ := os.ReadFile("./test_assets/rgw_data_sync_status.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "data", "sync", "status", "--source-zone", "sitea",
		"--cluster", "siteb", "--id", "sitea"}...).Return(string(output), nil).Once()
	common.ProcessExec = r

	status, err := GetRgwDataSyncStatus("sitea", "siteb", "sitea")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "sync", status.Info.Status)
	assert.Equal(s.T(), 128, status.Info.NumShards)
	assert.NotEmpty(s.T(), status.Markers)
	assert.Equal(s.T(), "incremental-sync", status.Markers[0].Val.Status)
}

func (s *RgwMultisiteSuite) TestGetRgwDataSyncStatusInvalidResponse() {
	r := mocks.NewRunner(s.T())

	output := `{"sync_status":{"info":{"status":"sync","num_shards":2},"markers":[{"key":5,"val":{"status":"incremental-sync","marker":""}}]}}`
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "data", "sync", "status", "--source-zone", "sitea"}...).Return(output, nil).Once()
	common.ProcessExec = r

	_, err := GetRgwDataSyncStatus("sitea", "", "")
	assert.Error(s.T(), err)
}

func (s *RgwMultisiteSuite) TestGetRgwMdlogStatus() {
	r := mocks.NewRunner(s.T())

	output, _ := os.ReadFile("./test_assets/rgw_mdlog_status.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "mdlog", "status"}...).Return(string(output), nil).Once()
	common.ProcessExec = r

	shards, err := GetRgwMdlogStatus("", "")
	assert.NoError(s.T(), err)
	assert.Len(s.T(), shards, 4)
	assert.Empty(s.T(), shards[0].Marker)
	assert.NotEmpty(s.T(), shards[2].Marker) // an active shard's head
}

func (s *RgwMultisiteSuite) TestGetRgwDatalogStatusRemote() {
	r := mocks.NewRunner(s.T())

	output, _ := os.ReadFile("./test_assets/rgw_datalog_status.json")
	r.On("RunCommand", []interface{}{
		"radosgw-admin", "datalog", "status", "--cluster", "siteb", "--id", "sitea"}...).Return(string(output), nil).Once()
	common.ProcessExec = r

	shards, err := GetRgwDatalogStatus("siteb", "sitea")
	assert.NoError(s.T(), err)
	assert.Len(s.T(), shards, 3)
	assert.NotEmpty(s.T(), shards[2].Marker)
}

func (s *RgwMultisiteSuite) TestValidateRgwSyncShards() {
	assert.NoError(s.T(), validateRgwSyncShards(3, []int{0, 1, 2}))
	assert.NoError(s.T(), validateRgwSyncShards(0, nil))
	assert.Error(s.T(), validateRgwSyncShards(-1, nil))
	assert.Error(s.T(), validateRgwSyncShards(0, []int{0}))
	assert.Error(s.T(), validateRgwSyncShards(3, []int{3}))
	assert.Error(s.T(), validateRgwSyncShards(3, []int{-1}))
}

func (s *RgwMultisiteSuite) TestValidateRgwMetadataSyncShards() {
	assert.NoError(s.T(), validateRgwMetadataSyncShards(2, []RgwMetadataSyncShard{
		{Key: 0}, {Key: 1},
	}))
	assert.Error(s.T(), validateRgwMetadataSyncShards(2, []RgwMetadataSyncShard{
		{Key: 5},
	}))
}

func (s *RgwMultisiteSuite) TestValidateRgwDataSyncShards() {
	assert.NoError(s.T(), validateRgwDataSyncShards(2, []RgwDataSyncShard{
		{Key: 0}, {Key: 1},
	}))
	assert.Error(s.T(), validateRgwDataSyncShards(2, []RgwDataSyncShard{
		{Key: 5},
	}))
}

func (s *RgwMultisiteSuite) TestComputeRgwMetadataSyncVerdictCaughtUp() {
	local := RgwMetadataSyncStatus{
		Info: RgwSyncInfo{Status: "sync", NumShards: 3, Period: "p1"},
		Markers: []RgwMetadataSyncShard{
			{Key: 0, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental, Marker: ""}},
			{Key: 1, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental, Marker: "1_100_5.1"}},
			{Key: 2, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental, Marker: "1_200_7.1"}},
		},
	}
	masterLog := []RgwLogShard{{Marker: ""}, {Marker: "1_100_5.1"}, {Marker: "1_200_7.1"}}

	verdict := ComputeRgwMetadataSyncVerdict(local, masterLog, "p1")
	assert.True(s.T(), verdict.CaughtUp)
	assert.Empty(s.T(), verdict.BehindShards)
	assert.Zero(s.T(), verdict.FullSyncShards)
	assert.False(s.T(), verdict.PeriodMismatch)
}

func (s *RgwMultisiteSuite) TestComputeRgwMetadataSyncVerdictBehind() {
	local := RgwMetadataSyncStatus{
		Info: RgwSyncInfo{Status: "sync", NumShards: 3, Period: "p1"},
		Markers: []RgwMetadataSyncShard{
			{Key: 0, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateFullSync, Marker: ""}},
			{Key: 1, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental, Marker: "1_100_5.1"}}, // behind
			{Key: 2, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental, Marker: "1_200_7.1"}}, // caught up
		},
	}
	masterLog := []RgwLogShard{{Marker: ""}, {Marker: "1_150_6.1"}, {Marker: "1_200_7.1"}}

	verdict := ComputeRgwMetadataSyncVerdict(local, masterLog, "p1")
	assert.False(s.T(), verdict.CaughtUp)
	assert.Equal(s.T(), []int{1}, verdict.BehindShards)
	assert.Equal(s.T(), 1, verdict.FullSyncShards)
}

func (s *RgwMultisiteSuite) TestComputeRgwMetadataSyncVerdictMissingShards() {
	// The zone claims 5 shards but only reported 3, all of them level.
	// The 2 it did not mention must still block a caught-up verdict.
	local := RgwMetadataSyncStatus{
		Info: RgwSyncInfo{Status: "sync", NumShards: 5, Period: "p1"},
		Markers: []RgwMetadataSyncShard{
			{Key: 0, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental, Marker: ""}},
			{Key: 1, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental, Marker: "1_100_5.1"}},
			{Key: 2, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental, Marker: "1_200_7.1"}},
		},
	}
	masterLog := []RgwLogShard{{Marker: ""}, {Marker: "1_100_5.1"}, {Marker: "1_200_7.1"}}

	verdict := ComputeRgwMetadataSyncVerdict(local, masterLog, "p1")
	assert.False(s.T(), verdict.CaughtUp)
	assert.Empty(s.T(), verdict.BehindShards)
	assert.Equal(s.T(), 2, verdict.FullSyncShards)
}

func (s *RgwMultisiteSuite) TestComputeRgwMetadataSyncVerdictInitStateNotCaughtUp() {
	// What a master reports, and what a secondary reports before sync
	// starts: nothing to check, so nothing was found wrong.
	local := RgwMetadataSyncStatus{
		Info:    RgwSyncInfo{Status: "init", NumShards: 0},
		Markers: nil,
	}

	verdict := ComputeRgwMetadataSyncVerdict(local, []RgwLogShard{{Marker: "1_100_5.1"}}, "")
	assert.False(s.T(), verdict.CaughtUp)
}

func (s *RgwMultisiteSuite) TestComputeRgwDataSyncVerdictBuildingFullSyncNotCaughtUp() {
	// A zone still building its full sync maps is not caught up either,
	// even though every marker it does have lines up with the source.
	local := RgwDataSyncStatus{
		Info: RgwSyncInfo{Status: "building-full-sync-maps", NumShards: 1},
		Markers: []RgwDataSyncShard{
			{Key: 0, Val: RgwDataSyncMarker{Status: "incremental-sync", Marker: "1_50_1.1"}},
		},
	}

	verdict := ComputeRgwDataSyncVerdict(local, []RgwLogShard{{Marker: "1_50_1.1"}})
	assert.False(s.T(), verdict.CaughtUp)
	assert.Empty(s.T(), verdict.BehindShards)
}

func (s *RgwMultisiteSuite) TestComputeRgwMetadataSyncVerdictPeerLogUnavailable() {
	// GetRgwMdlogStatus returns nil when it cannot reach the peer. Every
	// shard here is healthy and incremental, so comparing against nothing
	// would find no problems and wrongly read as caught up.
	markers := []RgwMetadataSyncShard{}
	for i := 0; i < 64; i++ {
		markers = append(markers, RgwMetadataSyncShard{
			Key: i,
			Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental, Marker: "1_100_5.1"},
		})
	}
	local := RgwMetadataSyncStatus{
		Info:    RgwSyncInfo{Status: "sync", NumShards: 64, Period: "p1"},
		Markers: markers,
	}

	verdict := ComputeRgwMetadataSyncVerdict(local, nil, "p1")
	assert.False(s.T(), verdict.CaughtUp)
	assert.True(s.T(), verdict.PeerLogUnavailable)
	assert.Empty(s.T(), verdict.BehindShards)
}

func (s *RgwMultisiteSuite) TestComputeRgwMetadataSyncVerdictEmptyPeerLogIsNotUnavailable() {
	// The whole fix rests on nil and empty being different: a peer that
	// genuinely has no log shards returns an empty non-nil slice, which
	// must still be compared rather than reported unavailable.
	local := RgwMetadataSyncStatus{
		Info:    RgwSyncInfo{Status: "sync", NumShards: 0, Period: "p1"},
		Markers: []RgwMetadataSyncShard{},
	}

	verdict := ComputeRgwMetadataSyncVerdict(local, []RgwLogShard{}, "p1")
	assert.False(s.T(), verdict.PeerLogUnavailable)
}

func (s *RgwMultisiteSuite) TestComputeRgwDataSyncVerdictPeerLogUnavailable() {
	local := RgwDataSyncStatus{
		Info: RgwSyncInfo{Status: "sync", NumShards: 3},
		Markers: []RgwDataSyncShard{
			{Key: 0, Val: RgwDataSyncMarker{Status: "incremental-sync", Marker: "1_50_1.1"}},
		},
	}

	verdict := ComputeRgwDataSyncVerdict(local, nil)
	assert.False(s.T(), verdict.CaughtUp)
	assert.True(s.T(), verdict.PeerLogUnavailable)
	assert.Empty(s.T(), verdict.BehindShards)
}

func (s *RgwMultisiteSuite) TestComputeRgwMetadataSyncVerdictPeriodMismatch() {
	local := RgwMetadataSyncStatus{
		Info: RgwSyncInfo{Status: "sync", NumShards: 1, Period: "p-old"},
		Markers: []RgwMetadataSyncShard{
			{Key: 0, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental, Marker: "x"}},
		},
	}

	verdict := ComputeRgwMetadataSyncVerdict(local, nil, "p-new")
	assert.False(s.T(), verdict.CaughtUp)
	assert.True(s.T(), verdict.PeriodMismatch)
	assert.Empty(s.T(), verdict.BehindShards) // comparison skipped, as upstream does
}

func (s *RgwMultisiteSuite) TestComputeRgwDataSyncVerdict() {
	local := RgwDataSyncStatus{
		Info: RgwSyncInfo{Status: "sync", NumShards: 3},
		Markers: []RgwDataSyncShard{
			{Key: 0, Val: RgwDataSyncMarker{Status: "incremental-sync", Marker: "1_50_1.1"}},
			{Key: 1, Val: RgwDataSyncMarker{Status: "full-sync", Marker: ""}},
			{Key: 5, Val: RgwDataSyncMarker{Status: "incremental-sync", Marker: ""}}, // out of log bounds
		},
	}
	sourceLog := []RgwLogShard{{Marker: "1_60_2.1"}, {Marker: ""}, {Marker: ""}}

	verdict := ComputeRgwDataSyncVerdict(local, sourceLog)
	assert.False(s.T(), verdict.CaughtUp)
	assert.Equal(s.T(), []int{0}, verdict.BehindShards) // shard 5 out of bounds: not counted
	assert.Equal(s.T(), 1, verdict.FullSyncShards)
}

func (s *RgwMultisiteSuite) TestComputeRgwDataSyncVerdictIgnoresRecoveringShards() {
	// Pins a known, deliberate gap: radosgw-admin would not call this
	// caught up if the shard were retrying failed objects, but reading
	// that costs one call per shard so we do not fetch it. If someone
	// wires it in, this test should fail and be updated on purpose.
	local := RgwDataSyncStatus{
		Info: RgwSyncInfo{Status: "sync", NumShards: 1},
		Markers: []RgwDataSyncShard{
			{Key: 0, Val: RgwDataSyncMarker{Status: "incremental-sync", Marker: "1_50_1.1"}},
		},
	}
	sourceLog := []RgwLogShard{{Marker: "1_50_1.1"}}

	verdict := ComputeRgwDataSyncVerdict(local, sourceLog)
	assert.True(s.T(), verdict.CaughtUp)
}

func (s *RgwMultisiteSuite) TestComputeRgwDataSyncVerdictMissingShards() {
	// Same idea on the data side: 5 shards claimed, 1 reported and level.
	local := RgwDataSyncStatus{
		Info: RgwSyncInfo{Status: "sync", NumShards: 5},
		Markers: []RgwDataSyncShard{
			{Key: 0, Val: RgwDataSyncMarker{Status: "incremental-sync", Marker: "1_50_1.1"}},
		},
	}
	sourceLog := []RgwLogShard{{Marker: "1_50_1.1"}}

	verdict := ComputeRgwDataSyncVerdict(local, sourceLog)
	assert.False(s.T(), verdict.CaughtUp)
	assert.Empty(s.T(), verdict.BehindShards)
	assert.Equal(s.T(), 4, verdict.FullSyncShards)
}

func (s *RgwMultisiteSuite) TestComputeRgwMetadataSyncVerdictNegativeShardKey() {
	// The compute functions are exported and take the parsed struct, so
	// they cannot assume validateRgwSyncShards rejected a negative key.
	// A negative key clears the upper bound check, so without a lower
	// bound it would index the peer log at -1 and panic. An out of range
	// key still counts toward the incremental total, as the high side
	// case does.
	local := RgwMetadataSyncStatus{
		Info: RgwSyncInfo{Status: "sync", NumShards: 2, Period: "p1"},
		Markers: []RgwMetadataSyncShard{
			{Key: -1, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental, Marker: ""}},
			{Key: 0, Val: RgwMetadataSyncMarker{State: RgwMetadataSyncStateIncremental, Marker: "1_100_5.1"}},
		},
	}
	masterLog := []RgwLogShard{{Marker: "1_200_7.1"}, {Marker: ""}}

	verdict := RgwSyncVerdict{}
	assert.NotPanics(s.T(), func() {
		verdict = ComputeRgwMetadataSyncVerdict(local, masterLog, "p1")
	})
	assert.False(s.T(), verdict.CaughtUp)
	assert.Equal(s.T(), []int{0}, verdict.BehindShards) // shard -1 skipped, not reported behind
}

func (s *RgwMultisiteSuite) TestComputeRgwDataSyncVerdictNegativeShardKey() {
	// Same lower bound guard on the data side.
	local := RgwDataSyncStatus{
		Info: RgwSyncInfo{Status: "sync", NumShards: 2},
		Markers: []RgwDataSyncShard{
			{Key: -1, Val: RgwDataSyncMarker{Status: "incremental-sync", Marker: ""}},
			{Key: 0, Val: RgwDataSyncMarker{Status: "incremental-sync", Marker: "1_50_1.1"}},
		},
	}
	sourceLog := []RgwLogShard{{Marker: "1_60_2.1"}, {Marker: ""}}

	verdict := RgwSyncVerdict{}
	assert.NotPanics(s.T(), func() {
		verdict = ComputeRgwDataSyncVerdict(local, sourceLog)
	})
	assert.False(s.T(), verdict.CaughtUp)
	assert.Equal(s.T(), []int{0}, verdict.BehindShards) // shard -1 skipped, not reported behind
}
