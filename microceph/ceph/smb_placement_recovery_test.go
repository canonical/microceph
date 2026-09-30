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
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

func TestSMBFreshPlacementFailureCleansStartedServices(t *testing.T) {
	for _, phase := range []string{"readiness", "database", "cancelled", "stop-fails"} {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			oldPaths, oldFetch, oldRunner, oldCheck, oldDB := constants.GetPathConst, fetchSMBSourceFunc, common.ProcessExec, smbPostPlacementCheckFunc, database.GroupedServicesQuery
			t.Cleanup(func() {
				constants.GetPathConst, fetchSMBSourceFunc, common.ProcessExec, smbPostPlacementCheckFunc, database.GroupedServicesQuery = oldPaths, oldFetch, oldRunner, oldCheck, oldDB
			})
			constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: filepath.Join(root, "conf")} }
			mockSMBPublicAddress(t, "192.0.2.0/24", "192.0.2.10")
			fetchSMBSourceFunc = func(context.Context, string) ([]byte, error) {
				return []byte(`{"shares":{"files":{"options":{"vfs objects":"ceph_new","ceph_new:proxy":"no"}}}}`), nil
			}
			running := false
			runner := mocks.NewRunner(t)
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
			common.ProcessExec = runner
			placement := &SMBServicePlacement{ClusterID: "files", ConfigURI: "rados://.smb/files/config.smb"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			require.NoError(t, placement.ServiceInit(ctx, nil))
			failure := errors.New("injected failure")
			var err error
			if phase == "database" {
				state := mocks.NewStateInterface(t)
				db := mocks.NewGroupedServiceQueryIntf(t)
				db.On("AddOrUpdate", ctx, state, "smb", "files", mock.Anything, mock.Anything).Return(failure).Once()
				database.GroupedServicesQuery = db
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
	root := t.TempDir()
	oldPaths, oldRunner, oldRetire := constants.GetPathConst, common.ProcessExec, retireSMBCTDBMemberFunc
	t.Cleanup(func() {
		constants.GetPathConst, common.ProcessExec, retireSMBCTDBMemberFunc = oldPaths, oldRunner, oldRetire
	})
	constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: filepath.Join(root, "conf")} }
	runtimeDir := filepath.Join(root, "samba")
	require.NoError(t, os.MkdirAll(runtimeDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "cluster-id"), []byte("files\n"), 0600))
	events := []string{}
	runner := mocks.NewRunner(t)
	for _, service := range []string{"smbd", "ctdb-nodes", "ctdbd"} {
		name := service
		runner.On("RunCommandContext", mock.Anything, "snapctl", "stop", "microceph."+service, "--disable").Run(func(mock.Arguments) { events = append(events, name) }).Return("", nil).Once()
	}
	common.ProcessExec = runner
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
	original := getRecordedSMBModeFunc
	t.Cleanup(func() { getRecordedSMBModeFunc = original })
	getRecordedSMBModeFunc = func(context.Context, interfaces.StateInterface, string) (*bool, error) { return mode, nil }
}

func TestSMBRemovalUsesRecordedModeWhenMetadataFileIsMissing(t *testing.T) {
	clustered := true
	stubSMBRecordedMode(t, &clustered)
	root := t.TempDir()
	oldPaths, oldRunner, oldRetire, oldDB := constants.GetPathConst, common.ProcessExec, retireSMBCTDBMemberFunc, database.GroupedServicesQuery
	t.Cleanup(func() {
		constants.GetPathConst, common.ProcessExec, retireSMBCTDBMemberFunc, database.GroupedServicesQuery = oldPaths, oldRunner, oldRetire, oldDB
	})
	constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: filepath.Join(root, "conf")} }
	dir := filepath.Join(root, "samba")
	require.NoError(t, os.MkdirAll(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cluster-id"), []byte("files\n"), 0600))
	events := []string{}
	runner := mocks.NewRunner(t)
	for _, service := range []string{"smbd", "ctdb-nodes", "ctdbd"} {
		name := service
		runner.On("RunCommandContext", mock.Anything, "snapctl", "stop", "microceph."+service, "--disable").Run(func(mock.Arguments) { events = append(events, name) }).Return("", nil).Once()
	}
	common.ProcessExec = runner
	retireSMBCTDBMemberFunc = func(context.Context, string) error {
		events = append(events, "retire")
		require.DirExists(t, dir)
		return nil
	}
	state := mocks.NewStateInterface(t)
	db := mocks.NewGroupedServiceQueryIntf(t)
	db.On("ExistsOnHost", mock.Anything, state, "smb", "files").Return(true, nil).Once()
	db.On("RemoveForHost", mock.Anything, state, "smb", "files").Return(nil).Once()
	db.On("GetGroupedServices", mock.Anything, state).Return([]database.GroupedService{{Service: "smb", GroupID: "files", Member: "other"}}, nil).Once()
	database.GroupedServicesQuery = db
	require.NoError(t, DisableSMB(context.Background(), state, "files"))
	require.Equal(t, []string{"smbd", "ctdb-nodes", "ctdbd", "retire"}, events)
}

func TestSMBRetirementUsesExistingMetadataIdentity(t *testing.T) {
	t.Setenv("SNAP", "/snap/microceph/current")
	root := t.TempDir()
	oldPaths, oldRunner := constants.GetPathConst, common.ProcessExec
	t.Cleanup(func() { constants.GetPathConst, common.ProcessExec = oldPaths, oldRunner })
	constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: filepath.Join(root, "conf")} }
	dir := filepath.Join(root, "samba")
	require.NoError(t, os.MkdirAll(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ctdb-rank"), []byte("2\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ctdb-identity"), []byte("smb.files.node-c\n"), 0600))
	runner := mocks.NewRunner(t)
	runner.On("RunCommandContext", mock.Anything, "/snap/microceph/current/bin/python3", "/snap/microceph/current/commands/ctdb_ready.py", "rados://.smb/files/cluster.meta.json", "files", "smb.files.node-c", "2", "--state", "gone").Return("", nil).Once()
	common.ProcessExec = runner
	require.NoError(t, retireSMBCTDBMember(context.Background(), "files"))
}

func TestSMBMissingCTDBFileDoesNotChangeRecordedMode(t *testing.T) {
	root := t.TempDir()
	oldPaths, oldRunner := constants.GetPathConst, common.ProcessExec
	t.Cleanup(func() { constants.GetPathConst, common.ProcessExec = oldPaths, oldRunner })
	constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: filepath.Join(root, "conf")} }
	runtimeDir := filepath.Join(root, "samba")
	require.NoError(t, os.MkdirAll(runtimeDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "cluster-id"), []byte("files\n"), 0600))
	runner := mocks.NewRunner(t)
	runner.On("RunCommandContext", mock.Anything, "snapctl", "is-connected", "smb-identity").Return("", nil).Once()
	runner.On("RunCommandContext", mock.Anything, "snapctl", "is-connected", "ctdb-run").Return("", nil).Once()
	common.ProcessExec = runner
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
	db := mocks.NewGroupedServiceQueryIntf(t)
	original := database.GroupedServicesQuery
	t.Cleanup(func() { database.GroupedServicesQuery = original })
	database.GroupedServicesQuery = db
	placement := &SMBServicePlacement{ClusterID: "files", unchanged: true, configDigest: "current", ctdbRanks: map[string]int{"smb.files.node-a": 0, "smb.files.node-new": 2}, nextCTDBRank: 3}
	db.On("AddOrUpdate", context.Background(), state, "smb", "files", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		group := args.Get(4).(database.SMBServiceGroupConfig)
		require.Equal(t, placement.ctdbRanks, group.CTDBRanks)
		require.Equal(t, 3, group.NextCTDBRank)
	}).Return(nil).Once()
	require.NoError(t, placement.DbUpdate(context.Background(), state))
}

func TestSMBCommandCancellationTerminatesLocalProcess(t *testing.T) {
	original := common.ProcessExec
	t.Cleanup(func() { common.ProcessExec = original })
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
	runner := mocks.NewRunner(t)
	original := common.ProcessExec
	t.Cleanup(func() { common.ProcessExec = original })
	common.ProcessExec = runner
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
