package ceph

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
	assert.Equal(s.T(), 1, status.Markers[0].Val.State) // incremental
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

func (s *RgwMultisiteSuite) TestComputeRgwMetadataSyncVerdictCaughtUp() {
	local := RgwMetadataSyncStatus{
		Info: RgwSyncInfo{Status: "sync", NumShards: 3, Period: "p1"},
		Markers: []RgwMetadataSyncShard{
			{Key: 0, Val: RgwMetadataSyncMarker{State: 1, Marker: ""}},
			{Key: 1, Val: RgwMetadataSyncMarker{State: 1, Marker: "1_100_5.1"}},
			{Key: 2, Val: RgwMetadataSyncMarker{State: 1, Marker: "1_200_7.1"}},
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
			{Key: 0, Val: RgwMetadataSyncMarker{State: 0, Marker: ""}},          // still full sync
			{Key: 1, Val: RgwMetadataSyncMarker{State: 1, Marker: "1_100_5.1"}}, // behind
			{Key: 2, Val: RgwMetadataSyncMarker{State: 1, Marker: "1_200_7.1"}}, // caught up
		},
	}
	masterLog := []RgwLogShard{{Marker: ""}, {Marker: "1_150_6.1"}, {Marker: "1_200_7.1"}}

	verdict := ComputeRgwMetadataSyncVerdict(local, masterLog, "p1")
	assert.False(s.T(), verdict.CaughtUp)
	assert.Equal(s.T(), []int{1}, verdict.BehindShards)
	assert.Equal(s.T(), 1, verdict.FullSyncShards)
}

func (s *RgwMultisiteSuite) TestComputeRgwMetadataSyncVerdictPeriodMismatch() {
	local := RgwMetadataSyncStatus{
		Info: RgwSyncInfo{Status: "sync", NumShards: 1, Period: "p-old"},
		Markers: []RgwMetadataSyncShard{
			{Key: 0, Val: RgwMetadataSyncMarker{State: 1, Marker: "x"}},
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
