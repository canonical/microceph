package ceph

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

func TestSMBFinalizationDeletesSharedCredentialLastAndRetries(t *testing.T) {
	for _, heldLease := range []bool{false, true} {
		name := "without managed lease"
		if heldLease {
			name = "with managed lease"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db := newSMBPlacementTestDB(t)
			state := interfaces.CephState{State: &mocks.MockState{Cert: &shared.CertInfo{}, DBObj: db}}
			snapshot := `{"desired_spec":{"features":["clustered"]},"ctdb_ranks":{"smb.files.node-a":0},"next_ctdb_rank":1}`
			if heldLease {
				snapshot = `{"desired_spec":{"features":["clustered"]},"ctdb_ranks":{"smb.files.node-a":0},"next_ctdb_rank":1,"managed_operation":{"token":"owner","expires_at":2000}}`
			}
			err := db.Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO service_groups(service,group_id,config) VALUES ('smb','files',?), ('smb','other','{}')`, snapshot)
				return err
			})
			require.NoError(t, err)
			checkFinalized := func(mock.Arguments) {
				config, found, err := database.GetSMBServiceGroupConfig(ctx, state, "files")
				require.NoError(t, err)
				require.Equal(t, heldLease, found)
				if heldLease {
					require.JSONEq(t, `{"desired_spec":null,"managed_operation":{"token":"owner","expires_at":2000}}`, config)
				}
				other, found, err := database.GetSMBServiceGroupConfig(ctx, state, "other")
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, "{}", other)
			}
			runner := smbTestRunner(t)
			smbTestCommand(runner, "ceph", "auth", "del", "client.smb.config.files").Run(checkFinalized).Return("", assert.AnError).Once()
			smbTestCommand(runner, "ceph", "auth", "del", "client.smb.config.files").Run(checkFinalized).Return("", nil).Twice()
			require.ErrorIs(t, FinalizeSMBServiceGroup(ctx, state, "files", snapshot), assert.AnError)
			// Retry with freshly observed group state, including when finalization
			// already removed the group. Ceph auth del succeeds for an absent key.
			for range 2 {
				current, _, err := database.GetSMBServiceGroupConfig(ctx, state, "files")
				require.NoError(t, err)
				require.NoError(t, FinalizeSMBServiceGroup(ctx, state, "files", current))
			}
		})
	}
}

func TestSMBFinalizationRejectsBeforeDeletingSharedCredential(t *testing.T) {
	for _, reason := range []string{"records remain", "state changed"} {
		t.Run(reason, func(t *testing.T) {
			ctx := context.Background()
			db := newSMBPlacementTestDB(t)
			state := interfaces.CephState{State: &mocks.MockState{Cert: &shared.CertInfo{}, DBObj: db}}
			err := db.Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO service_groups(id,service,group_id,config) VALUES (1,'smb','files','{}')`)
				if err != nil {
					return err
				}
				if reason == "records remain" {
					_, err = tx.ExecContext(ctx, `INSERT INTO grouped_services(service_group_id,member_id) VALUES (1,1)`)
				}
				return err
			})
			require.NoError(t, err)
			expected := "{}"
			if reason == "state changed" {
				expected = `{"desired_spec":{}}`
			}
			runner := smbTestRunner(t)
			require.ErrorContains(t, FinalizeSMBServiceGroup(ctx, state, "files", expected), reason)
			runner.AssertNotCalled(t, "RunCommandContext", mock.Anything, "ceph", "auth", "del", "client.smb.config.files")
			config, found, err := database.GetSMBServiceGroupConfig(ctx, state, "files")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, "{}", config)
		})
	}
}

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
	runner := smbTestRunner(t)
	for _, service := range []string{"smbd", "ctdb-nodes", "ctdbd"} {
		smbTestCommand(runner, "snapctl", "stop", "microceph."+service, "--disable").Return("", nil).Twice()
	}
	for range 2 {
		smbTestCommand(runner, "/snap/microceph/current/bin/python3", "/snap/microceph/current/commands/ctdb_ready.py",
			"rados://.smb/files/cluster.meta.json", "files", "smb.files.node-a", "0", "--state", "gone").Return("", nil).Once()
	}

	err := DisableSMB(context.Background(), state, "files")
	require.ErrorIs(t, err, assert.AnError)
	require.FileExists(t, filepath.Join(runtime, "ctdb-rank"))
	require.FileExists(t, filepath.Join(runtime, "ctdb-identity"))
	require.FileExists(t, dataKey)

	require.NoError(t, DisableSMB(context.Background(), state, "files"))
	require.NoDirExists(t, runtime)
	require.NoFileExists(t, dataKey)
}
