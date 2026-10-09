package ceph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

func TestSMBFreshPlacementFailureCleansStartedServices(t *testing.T) {
	for _, phase := range []string{"readiness", "database", "cancelled", "stop-fails"} {
		t.Run(phase, func(t *testing.T) {
			root, _, _ := smbTestPaths(t, false)
			preserveSMBTestGlobal(t, &smbPostPlacementCheckFunc)
			mockSMBPublicAddress(t, "192.0.2.0/24", "192.0.2.10")
			smbTestSource(t, smbTestContainer)
			running := false
			runner := smbTestRunner(t)
			runner.On("RunCommandContext", mock.Anything, "ceph", "auth", "get", "client.smb.fs.cluster.files").Return("key", nil).Once()
			runner.On("RunCommandContext", mock.Anything, "snapctl", "services", "microceph.smbd").Return("microceph.smbd disabled inactive", nil).Once()
			runner.On("RunCommandContext", mock.Anything, "snapctl", "start", "microceph.smbd", "--enable").Run(func(mock.Arguments) { running = true }).Return("", nil).Once()
			var stopErr error
			if phase == "stop-fails" {
				stopErr = errors.New("stop failed")
			}
			runner.On("RunCommandContext", mock.Anything, "snapctl", "stop", "microceph.smbd", "--disable").Run(func(args mock.Arguments) {
				cleanupCtx := args.Get(0).(context.Context)
				require.NoError(t, cleanupCtx.Err())
				_, bounded := cleanupCtx.Deadline()
				require.True(t, bounded)
				if stopErr == nil {
					running = false
				}
			}).Return("", stopErr).Once()
			placement := &SMBServicePlacement{ClusterID: "files", ConfigURI: "rados://.smb/files/config.smb"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			require.NoError(t, placement.ServiceInit(ctx, nil))
			failure := errors.New("injected failure")
			var err error
			if phase == "database" {
				state := mocks.NewStateInterface(t)
				db := smbTestGroupedDB(t)
				db.On("AddOrUpdate", ctx, state, "smb", "files", mock.Anything, mock.Anything).Return(failure).Once()
				err = placement.DbUpdate(ctx, state)
			} else {
				if phase == "cancelled" {
					cancel()
				}
				smbPostPlacementCheckFunc = func(context.Context, string) error { return failure }
				err = placement.PostPlacementCheck(nil)
			}
			require.ErrorIs(t, err, failure)
			marker := filepath.Join(root, "samba", "cluster-id")
			if stopErr == nil {
				assert.False(t, running)
				assert.NoFileExists(t, marker)
			} else {
				assert.True(t, running)
				assert.FileExists(t, marker)
				assert.ErrorContains(t, err, "stop failed")
			}
		})
	}
}

func TestSMBFreshClusteredRollbackRetiresAfterStopping(t *testing.T) {
	_, _, runtimeDir := smbTestPaths(t, false)
	preserveSMBTestGlobal(t, &retireSMBCTDBMemberFunc)
	smbTestWrite(t, filepath.Join(runtimeDir, "cluster-id"), "files\n")
	events := []string{}
	runner := smbTestRunner(t)
	for _, service := range []string{"smbd", "ctdb-nodes", "ctdbd"} {
		name := service
		runner.On("RunCommandContext", mock.Anything, "snapctl", "stop", "microceph."+service, "--disable").Run(func(mock.Arguments) { events = append(events, name) }).Return("", nil).Once()
	}
	retireSMBCTDBMemberFunc = func(ctx context.Context, id string) error {
		require.NoError(t, ctx.Err())
		require.Equal(t, "files", id)
		require.FileExists(t, filepath.Join(runtimeDir, "cluster-id"))
		events = append(events, "retire")
		return nil
	}
	placement := &SMBServicePlacement{ClusterID: "files", Features: []string{"clustered"}, freshPlacement: true, startedServices: []string{"ctdbd", "ctdb-nodes", "smbd"}}
	err := placement.rollbackPlacement(assert.AnError)
	require.ErrorIs(t, err, assert.AnError)
	require.Equal(t, []string{"smbd", "ctdb-nodes", "ctdbd", "retire"}, events)
	require.NoDirExists(t, runtimeDir)
}

func stubSMBRecordedMode(t *testing.T, mode *bool) {
	t.Helper()
	preserveSMBTestGlobal(t, &getRecordedSMBModeFunc)
	getRecordedSMBModeFunc = func(context.Context, interfaces.StateInterface, string) (*bool, error) { return mode, nil }
}

func TestSMBRemovalUsesRecordedModeWhenMetadataFileIsMissing(t *testing.T) {
	clustered := true
	stubSMBRecordedMode(t, &clustered)
	_, _, dir := smbTestPaths(t, false)
	preserveSMBTestGlobal(t, &retireSMBCTDBMemberFunc)
	smbTestWrite(t, filepath.Join(dir, "cluster-id"), "files\n")
	events := []string{}
	runner := smbTestRunner(t)
	for _, service := range []string{"smbd", "ctdb-nodes", "ctdbd"} {
		name := service
		runner.On("RunCommandContext", mock.Anything, "snapctl", "stop", "microceph."+service, "--disable").Run(func(mock.Arguments) { events = append(events, name) }).Return("", nil).Once()
	}
	retireSMBCTDBMemberFunc = func(context.Context, string) error {
		events = append(events, "retire")
		require.DirExists(t, dir)
		return nil
	}
	state := mocks.NewStateInterface(t)
	db := smbTestGroupedDB(t)
	db.On("ExistsOnHost", mock.Anything, state, "smb", "files").Return(true, nil).Once()
	db.On("RemoveForHost", mock.Anything, state, "smb", "files").Return(nil).Once()
	db.On("GetGroupedServices", mock.Anything, state).Return([]database.GroupedService{{Service: "smb", GroupID: "files", Member: "other"}}, nil).Once()
	require.NoError(t, DisableSMB(context.Background(), state, "files"))
	require.Equal(t, []string{"smbd", "ctdb-nodes", "ctdbd", "retire"}, events)
}

func TestSMBRetirementUsesExistingMetadataIdentity(t *testing.T) {
	t.Setenv("SNAP", "/snap/microceph/current")
	_, _, dir := smbTestPaths(t, false)
	smbTestWrite(t, filepath.Join(dir, "ctdb-rank"), "2\n")
	smbTestWrite(t, filepath.Join(dir, "ctdb-identity"), "smb.files.node-c\n")
	runner := smbTestRunner(t)
	runner.On("RunCommandContext", mock.Anything, "/snap/microceph/current/bin/python3", "/snap/microceph/current/commands/ctdb_ready.py", "rados://.smb/files/cluster.meta.json", "files", "smb.files.node-c", "2", "--state", "gone").Return("", nil).Once()
	require.NoError(t, retireSMBCTDBMember(context.Background(), "files"))
}

func TestSMBMissingCTDBFileDoesNotChangeRecordedMode(t *testing.T) {
	_, _, runtimeDir := smbTestPaths(t, false)
	smbTestWrite(t, filepath.Join(runtimeDir, "cluster-id"), "files\n")
	runner := smbTestRunner(t)
	runner.On("RunCommandContext", mock.Anything, "snapctl", "is-connected", "smb-identity").Return("", nil).Once()
	runner.On("RunCommandContext", mock.Anything, "snapctl", "is-connected", "ctdb-run").Return("", nil).Once()
	clustered := true
	placement := &SMBServicePlacement{
		ClusterID: "files", Features: []string{"clustered"}, recordedClustered: &clustered,
		ctdb:       &smbCTDBPlacement{Rank: 0, Identity: "smb.files.node-a"},
		configData: &smbSourceData{container: []byte(`{"shares":{}}`)},
	}
	require.NoError(t, placement.HospitalityCheck(nil))
	// Both local preflight and config generation use the recorded/incoming mode,
	// not existence of the generated metadata file.
	require.NoError(t, placement.checkClusteringMode())
	require.NoError(t, materializeSMBConfig(context.Background(), placement))
	require.FileExists(t, filepath.Join(runtimeDir, "ctdb.json"))
}

func TestSMBUnchangedMemberStillPersistsReservedRanks(t *testing.T) {
	state := mocks.NewStateInterface(t)
	db := smbTestGroupedDB(t)
	spec := `{"service_type":"smb","service_id":"files","placement":{"hosts":["node-a","node-new"],"count":2},"spec":{"cluster_id":"files","config_uri":"rados://.smb/files/config.smb","features":["clustered"],"cluster_meta_uri":"rados://.smb/files/cluster.meta.json","cluster_lock_uri":"rados://.smb/files/cluster.meta.lock"}}`
	placement := &SMBServicePlacement{ClusterID: "files", unchanged: true, configDigest: "current", upstreamSpec: []byte(spec), ctdbRanks: map[string]int{"smb.files.node-a": 0, "smb.files.node-new": 2}, nextCTDBRank: 3}
	db.On("AddOrUpdate", context.Background(), state, "smb", "files", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		group := args.Get(4).(database.SMBServiceGroupConfig)
		info := args.Get(5).(database.SMBServiceInfo)
		require.Equal(t, placement.ctdbRanks, group.CTDBRanks)
		require.Equal(t, 3, group.NextCTDBRank)
		require.JSONEq(t, spec, string(group.DesiredSpec))
		require.JSONEq(t, spec, string(info.AppliedSpec))
	}).Return(nil).Once()
	require.NoError(t, placement.DbUpdate(context.Background(), state))
}

func TestSMBCommandCancellationTerminatesLocalProcess(t *testing.T) {
	preserveSMBTestGlobal(t, &common.ProcessExec)
	common.ProcessExec = common.RunnerImpl{}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := runSMBCommand(ctx, "sh", "-c", "exec sleep 30")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestSMBSourceFetchesUseBoundedContext(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "preserved")
	bounded := mock.MatchedBy(func(got context.Context) bool {
		_, ok := got.Deadline()
		return ok && got.Value(key{}) == "preserved"
	})
	runner := smbTestRunner(t)
	runner.On("RunCommandContext", bounded, "ceph", "config-key", "get", "smb/config/files/users.json").Return("users", nil).Once()
	runner.On("RunCommandContext", bounded, "rados", "--pool", ".smb", "-N", "files", "get", "config.smb", mock.Anything).Run(func(args mock.Arguments) {
		require.NoError(t, os.WriteFile(args.String(8), []byte("config"), 0600))
	}).Return("", nil).Once()
	data, err := fetchSMBSource(ctx, "rados:mon-config-key:smb/config/files/users.json")
	require.NoError(t, err)
	require.Equal(t, "users", string(data))
	data, err = fetchSMBSource(ctx, "rados://.smb/files/config.smb")
	require.NoError(t, err)
	require.Equal(t, "config", string(data))
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = fetchSMBSource(cancelled, "rados://.smb/files/config.smb")
	require.ErrorIs(t, err, context.Canceled)
}
