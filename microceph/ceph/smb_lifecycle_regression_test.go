package ceph

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
)

func TestGetLocalSMBClusterIDReadsOwnershipMarkerWithoutReceipt(t *testing.T) {
	_, _, runtime := smbTestPaths(t, false)

	clusterID, err := GetLocalSMBClusterID(context.Background())
	require.NoError(t, err)
	require.Empty(t, clusterID)

	smbTestWrite(t, filepath.Join(runtime, "cluster-id"), "files\n")
	clusterID, err = GetLocalSMBClusterID(context.Background())
	require.NoError(t, err)
	require.Equal(t, "files", clusterID)
}

func TestDisableSMBWaitsForNodeLocalLifecycleLock(t *testing.T) {
	_, _, runtime := smbTestPaths(t, false)
	smbTestWrite(t, filepath.Join(runtime, "cluster-id"), "files\n")
	mode := false
	stubSMBRecordedMode(t, &mode)

	state := mocks.NewStateInterface(t)
	db := smbTestGroupedDB(t)
	db.On("ExistsOnHost", mock.Anything, state, "smb", "files").Return(true, nil).Once()
	db.On("RemoveForHost", mock.Anything, state, "smb", "files").Return(nil).Once()
	db.On("ExistsOnHost", mock.Anything, state, "smb", "files").Return(false, nil).Once()
	db.On("GetGroupedServicesOnHost", mock.Anything, state).Return([]database.GroupedService{}, nil).Once()
	runner := smbTestRunner(t)
	smbTestCommand(runner, "snapctl", "stop", "microceph.smbd", "--disable").Return("", nil).Once()

	serviceStartMu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- DisableSMB(context.Background(), state, "files")
	}()
	select {
	case err := <-done:
		serviceStartMu.Unlock()
		t.Fatalf("DisableSMB bypassed serviceStartMu: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	serviceStartMu.Unlock()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("DisableSMB did not resume after serviceStartMu was released")
	}
	// The completed teardown must not stop services or delete the receipt again.
	require.NoError(t, DisableSMB(context.Background(), state, "files"))
}

func TestDisableSMBRetriesDatabaseRemovalBeforeDeletingRetirementInputs(t *testing.T) {
	t.Setenv("SNAP", "/snap/microceph/current")
	_, conf, runtime := smbTestPaths(t, false)
	smbTestWrite(t, filepath.Join(runtime, "cluster-id"), "files\n")
	smbTestWrite(t, filepath.Join(runtime, "ctdb.json"), "{}")
	smbTestWrite(t, filepath.Join(runtime, "ctdb-rank"), "0\n")
	smbTestWrite(t, filepath.Join(runtime, "ctdb-identity"), "smb.files.node-a\n")
	dataKey := filepath.Join(conf, "ceph.client.smb.fs.cluster.files.keyring")
	smbTestWrite(t, dataKey, "keyring")
	mode := true
	stubSMBRecordedMode(t, &mode)

	state := mocks.NewStateInterface(t)
	db := smbTestGroupedDB(t)
	db.On("ExistsOnHost", mock.Anything, state, "smb", "files").Return(true, nil).Twice()
	db.On("RemoveForHost", mock.Anything, state, "smb", "files").Return(assert.AnError).Once()
	db.On("RemoveForHost", mock.Anything, state, "smb", "files").Return(nil).Once()
	db.On("GetGroupedServices", mock.Anything, state).Return([]database.GroupedService{}, nil).Once()
	runner := smbTestRunner(t)
	for _, service := range []string{"smbd", "ctdb-nodes", "ctdbd"} {
		smbTestCommand(runner, "snapctl", "stop", "microceph."+service, "--disable").Return("", nil).Twice()
	}
	for range 2 {
		smbTestCommand(runner, "/snap/microceph/current/bin/python3", "/snap/microceph/current/commands/ctdb_ready.py",
			"rados://.smb/files/cluster.meta.json", "files", "smb.files.node-a", "0", "--state", "gone").Return("", nil).Once()
	}
	smbTestCommand(runner, "ceph", "auth", "del", "client.smb.config.files").Return("", nil).Once()

	err := DisableSMB(context.Background(), state, "files")
	require.ErrorIs(t, err, assert.AnError)
	require.FileExists(t, filepath.Join(runtime, "ctdb-rank"))
	require.FileExists(t, filepath.Join(runtime, "ctdb-identity"))
	require.FileExists(t, dataKey)

	require.NoError(t, DisableSMB(context.Background(), state, "files"))
	require.NoDirExists(t, runtime)
	require.NoFileExists(t, dataKey)
}
