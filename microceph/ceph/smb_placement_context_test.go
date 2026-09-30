package ceph

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

func newSMBPlacementTestDB(t *testing.T) *mocks.MockDB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`
CREATE TABLE service_groups (id INTEGER PRIMARY KEY, service TEXT, group_id TEXT, config TEXT);
CREATE TABLE grouped_services (service_group_id INTEGER, member_id INTEGER);
CREATE TABLE core_cluster_members (id INTEGER PRIMARY KEY, name TEXT);`)
	require.NoError(t, err)
	return &mocks.MockDB{TxFn: func(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		err = fn(ctx, tx)
		if err != nil {
			return err
		}
		return tx.Commit()
	}}
}

func TestSMBPopulateParamsDoesNotAccessDatabase(t *testing.T) {
	// The population hook has no context. Database work must wait for ServiceInit.
	state := mocks.NewStateInterface(t)
	placement := &SMBServicePlacement{}
	err := placement.PopulateParams(state, `{"cluster_id":"files","config_uri":"rados://.smb/files/config.smb"}`)
	require.NoError(t, err)
}

func TestSMBServiceInitPreservesPreflightContext(t *testing.T) {
	originalPaths, originalFetch := constants.GetPathConst, fetchConfigDb
	t.Cleanup(func() { constants.GetPathConst, fetchConfigDb = originalPaths, originalFetch })
	root := t.TempDir()
	constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: filepath.Join(root, "conf")} }
	fetchConfigDb = func(context.Context, interfaces.StateInterface) (map[string]string, error) {
		t.Error("network lookup must not run after a failed mode preflight")
		return nil, assert.AnError
	}

	type contextKey struct{}
	// MicroCluster stores its required logger in the incoming context.
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), contextKey{}, "request-value"), time.Minute)
	defer cancel()
	called := false
	state := mocks.NewStateInterface(t)
	state.On("ClusterState").Return(&mocks.MockState{DBObj: &mocks.MockDB{
		TxFn: func(got context.Context, _ func(context.Context, *sql.Tx) error) error {
			called = true
			require.Equal(t, "request-value", got.Value(contextKey{}))
			deadline, ok := got.Deadline()
			require.True(t, ok)
			require.LessOrEqual(t, time.Until(deadline), 30*time.Second)
			cancel()
			require.ErrorIs(t, got.Err(), context.Canceled)
			return assert.AnError
		},
	}}).Maybe()
	placement := &SMBServicePlacement{ClusterID: "files"}
	require.ErrorIs(t, placement.ServiceInit(ctx, state), assert.AnError)
	require.True(t, called, "ServiceInit must run the database preflight")
	require.NoDirExists(t, filepath.Join(root, "samba"))
}

func TestSMBServiceInitRejectsRecordedModeChangeBeforeMutation(t *testing.T) {
	originalPaths, originalFetch := constants.GetPathConst, fetchConfigDb
	t.Cleanup(func() { constants.GetPathConst, fetchConfigDb = originalPaths, originalFetch })
	root := t.TempDir()
	constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: filepath.Join(root, "conf")} }
	fetchConfigDb = func(context.Context, interfaces.StateInterface) (map[string]string, error) {
		t.Error("network lookup must not precede mode validation")
		return nil, assert.AnError
	}
	db := newSMBPlacementTestDB(t)
	err := db.Transaction(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO service_groups (service, group_id, config)
VALUES ('smb', 'files', '{"desired_spec":{"spec":{"features":["clustered"]}}}')`)
		return err
	})
	require.NoError(t, err)
	state := mocks.NewStateInterface(t)
	state.On("ClusterState").Return(&mocks.MockState{ClusterName: "node-a", DBObj: db}).Maybe()
	placement := &SMBServicePlacement{ClusterID: "files"}
	require.ErrorContains(t, placement.ServiceInit(context.Background(), state), "clustering mode cannot be changed")
	require.NoDirExists(t, filepath.Join(root, "samba"))
}
