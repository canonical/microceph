package database

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupLifecycleDB creates an in-memory SQLite database with the cluster_lifecycle
// table and seeds the singleton row, returning a *sql.DB for testing.
func setupLifecycleDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`
CREATE TABLE cluster_lifecycle (
  id                    INTEGER PRIMARY KEY NOT NULL DEFAULT 1,
  ceph_bootstrapped     INTEGER NOT NULL DEFAULT 0,
  ceph_bootstrap_state  TEXT    NOT NULL DEFAULT 'not_bootstrapped',
  ceph_bootstrap_target TEXT,
  detail                TEXT,
  CONSTRAINT singleton CHECK (id = 1)
);
INSERT INTO cluster_lifecycle (id) VALUES (1);
`)
	require.NoError(t, err)
	return db
}

// setupLifecycleDBWithConfig creates an in-memory SQLite database with the
// cluster_lifecycle table, the config table, and seeds the singleton row.
// If withLegacyConfig is true, it also inserts fsid and keyring.client.admin
// config rows to simulate a legacy bootstrapped cluster.
func setupLifecycleDBWithConfig(t *testing.T, withLegacyConfig bool) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`
CREATE TABLE config (
  id    INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
  key   TEXT NOT NULL,
  value TEXT NOT NULL,
  UNIQUE(key)
);
CREATE TABLE cluster_lifecycle (
  id                    INTEGER PRIMARY KEY NOT NULL DEFAULT 1,
  ceph_bootstrapped     INTEGER NOT NULL DEFAULT 0,
  ceph_bootstrap_state  TEXT    NOT NULL DEFAULT 'not_bootstrapped',
  ceph_bootstrap_target TEXT,
  detail                TEXT,
  CONSTRAINT singleton CHECK (id = 1)
);
INSERT INTO cluster_lifecycle (id) VALUES (1);
`)
	require.NoError(t, err)

	if withLegacyConfig {
		_, err = db.Exec(`
INSERT INTO config (key, value) VALUES ('fsid', 'deadbeef-0000-0000-0000-000000000000');
INSERT INTO config (key, value) VALUES ('keyring.client.admin', 'AQABfakekey==');
`)
		require.NoError(t, err)
	}
	return db
}

// TestSchemaUpdate8BackfillLegacyBootstrapped verifies that schemaUpdate8's
// backfill logic marks the lifecycle row as bootstrapped when legacy config
// rows (fsid + keyring.client.admin) exist (FIX 2).
func TestSchemaUpdate8BackfillLegacyBootstrapped(t *testing.T) {
	db := setupLifecycleDBWithConfig(t, true)

	// Run the backfill SQL (same logic as schemaUpdate8's UPDATE).
	_, err := db.Exec(`
UPDATE cluster_lifecycle
   SET ceph_bootstrapped = 1, ceph_bootstrap_state = 'bootstrapped'
 WHERE id = 1
   AND EXISTS (SELECT 1 FROM config WHERE key = 'fsid')
   AND EXISTS (SELECT 1 FROM config WHERE key = 'keyring.client.admin');
`)
	require.NoError(t, err)

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	lc, err := GetClusterLifecycle(context.Background(), tx)
	require.NoError(t, err)
	assert.True(t, lc.CephBootstrapped, "legacy bootstrapped cluster must be backfilled to bootstrapped")
	assert.Equal(t, CephStateBootstrapped, lc.CephBootstrapState)
}

// TestSchemaUpdate8NoBackfillWithoutConfig verifies that without legacy config
// rows, the lifecycle row stays not_bootstrapped (FIX 2 negative case).
func TestSchemaUpdate8NoBackfillWithoutConfig(t *testing.T) {
	db := setupLifecycleDBWithConfig(t, false)

	// Run the backfill SQL.
	_, err := db.Exec(`
UPDATE cluster_lifecycle
   SET ceph_bootstrapped = 1, ceph_bootstrap_state = 'bootstrapped'
 WHERE id = 1
   AND EXISTS (SELECT 1 FROM config WHERE key = 'fsid')
   AND EXISTS (SELECT 1 FROM config WHERE key = 'keyring.client.admin');
`)
	require.NoError(t, err)

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	lc, err := GetClusterLifecycle(context.Background(), tx)
	require.NoError(t, err)
	assert.False(t, lc.CephBootstrapped, "cluster without legacy config must stay not_bootstrapped")
	assert.Equal(t, CephStateNotBootstrapped, lc.CephBootstrapState)
}

// TestSetClusterLifecycleDefault verifies the default lifecycle state after schema creation.
func TestGetClusterLifecycleDefault(t *testing.T) {
	db := setupLifecycleDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	lc, err := GetClusterLifecycle(context.Background(), tx)
	require.NoError(t, err)
	assert.False(t, lc.CephBootstrapped)
	assert.Equal(t, CephStateNotBootstrapped, lc.CephBootstrapState)
	assert.Empty(t, lc.CephBootstrapTarget)
	assert.Empty(t, lc.Detail)
}

// TestSetClusterLifecycleInProgress verifies setting the state to in_progress with a target.
func TestSetClusterLifecycleInProgress(t *testing.T) {
	db := setupLifecycleDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	err = SetClusterLifecycle(context.Background(), tx, ClusterLifecycle{
		CephBootstrapped:    false,
		CephBootstrapState:  CephStateInProgress,
		CephBootstrapTarget: "node-b",
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	tx2, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx2.Rollback() }()

	lc, err := GetClusterLifecycle(context.Background(), tx2)
	require.NoError(t, err)
	assert.False(t, lc.CephBootstrapped)
	assert.Equal(t, CephStateInProgress, lc.CephBootstrapState)
	assert.Equal(t, "node-b", lc.CephBootstrapTarget)
}

// TestSetClusterLifecycleBootstrapped verifies setting the state to bootstrapped.
func TestSetClusterLifecycleBootstrapped(t *testing.T) {
	db := setupLifecycleDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	err = SetClusterLifecycle(context.Background(), tx, ClusterLifecycle{
		CephBootstrapped:   true,
		CephBootstrapState: CephStateBootstrapped,
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	tx2, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx2.Rollback() }()

	lc, err := GetClusterLifecycle(context.Background(), tx2)
	require.NoError(t, err)
	assert.True(t, lc.CephBootstrapped)
	assert.Equal(t, CephStateBootstrapped, lc.CephBootstrapState)
}

// TestSetClusterLifecycleFailed verifies setting the state to failed with detail.
func TestSetClusterLifecycleFailed(t *testing.T) {
	db := setupLifecycleDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	err = SetClusterLifecycle(context.Background(), tx, ClusterLifecycle{
		CephBootstrapped:   false,
		CephBootstrapState: CephStateFailed,
		Detail:             "keyring creation failed",
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	tx2, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx2.Rollback() }()

	lc, err := GetClusterLifecycle(context.Background(), tx2)
	require.NoError(t, err)
	assert.False(t, lc.CephBootstrapped)
	assert.Equal(t, CephStateFailed, lc.CephBootstrapState)
	assert.Contains(t, lc.Detail, "keyring creation failed")
}

// TestSetClusterLifecycleSingletonUpsert (T4) verifies that calling
// SetClusterLifecycle twice updates the same singleton row (no duplication),
// and the CHECK(id=1) constraint holds.
func TestSetClusterLifecycleSingletonUpsert(t *testing.T) {
	db := setupLifecycleDB(t)

	// First update.
	tx1, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = SetClusterLifecycle(context.Background(), tx1, ClusterLifecycle{
		CephBootstrapped:   false,
		CephBootstrapState: CephStateInProgress,
	})
	require.NoError(t, err)
	require.NoError(t, tx1.Commit())

	// Second update.
	tx2, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = SetClusterLifecycle(context.Background(), tx2, ClusterLifecycle{
		CephBootstrapped:   true,
		CephBootstrapState: CephStateBootstrapped,
	})
	require.NoError(t, err)
	require.NoError(t, tx2.Commit())

	// Verify only one row exists with the latest state.
	var count int
	err = db.QueryRow(`SELECT count(*) FROM cluster_lifecycle`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "singleton table must have exactly one row")

	tx3, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx3.Rollback() }()

	lc, err := GetClusterLifecycle(context.Background(), tx3)
	require.NoError(t, err)
	assert.True(t, lc.CephBootstrapped)
	assert.Equal(t, CephStateBootstrapped, lc.CephBootstrapState)
}
