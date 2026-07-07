package database

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupLifecycleDB creates an in-memory SQLite database with the config
// table (the pre-existing dependency) and runs the real schemaUpdate8
// migration to create the cluster_lifecycle table and seed the singleton row.
// This exercises the exact SQL that runs on a deployed cluster's upgrade.
func setupLifecycleDB(t *testing.T) *sql.DB {
	t.Helper()
	return setupLifecycleDBWithConfig(t, false)
}

// setupLifecycleDBWithConfig creates an in-memory SQLite database with the
// config table, optionally seeds legacy bootstrapped config rows (fsid +
// keyring.client.admin), and then runs the real schemaUpdate8 migration.
// schemaUpdate8 creates the cluster_lifecycle table, seeds the singleton row,
// and runs the legacy-bootstrapped backfill (which finds the config rows if
// they were inserted before the migration runs).
func setupLifecycleDBWithConfig(t *testing.T, withLegacyConfig bool) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	// Create the config table — the pre-existing dependency that
	// schemaUpdate8's backfill references via EXISTS subqueries.
	_, err = db.Exec(`
CREATE TABLE config (
  id    INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
  key   TEXT NOT NULL,
  value TEXT NOT NULL,
  UNIQUE(key)
);
`)
	require.NoError(t, err)

	if withLegacyConfig {
		_, err = db.Exec(`
INSERT INTO config (key, value) VALUES ('fsid', 'deadbeef-0000-0000-0000-000000000000');
INSERT INTO config (key, value) VALUES ('keyring.client.admin', 'AQABfakekey==');
`)
		require.NoError(t, err)
	}

	// Run the real schemaUpdate8 migration: creates cluster_lifecycle, seeds
	// the singleton row, and runs the legacy-bootstrapped backfill.
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = schemaUpdate8(context.Background(), tx)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	return db
}

// TestSchemaUpdate8BackfillLegacyBootstrapped verifies that schemaUpdate8's
// backfill logic marks the lifecycle row as bootstrapped when legacy config
// rows (fsid + keyring.client.admin) exist. The setup runs the real
// schemaUpdate8 migration, so this exercises the exact shipped SQL.
func TestSchemaUpdate8BackfillLegacyBootstrapped(t *testing.T) {
	db := setupLifecycleDBWithConfig(t, true)

	// schemaUpdate8 (run inside setupLifecycleDBWithConfig) created the
	// cluster_lifecycle table and ran the backfill UPDATE that marks legacy
	// bootstrapped clusters. Verify the result.
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	lc, err := GetClusterLifecycle(context.Background(), tx)
	require.NoError(t, err)
	assert.Equal(t, CephStateBootstrapped, lc.CephBootstrapState, "legacy bootstrapped cluster must be backfilled to bootstrapped")
}

// TestSchemaUpdate8NoBackfillWithoutConfig verifies that without legacy config
// rows, schemaUpdate8 leaves the lifecycle row as not_bootstrapped. The setup
// runs the real schemaUpdate8 migration, so this exercises the exact shipped SQL.
func TestSchemaUpdate8NoBackfillWithoutConfig(t *testing.T) {
	db := setupLifecycleDBWithConfig(t, false)

	// schemaUpdate8 (run inside setupLifecycleDBWithConfig) created the
	// cluster_lifecycle table. Without legacy config rows the backfill is a
	// no-op, so the row stays not_bootstrapped.
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	lc, err := GetClusterLifecycle(context.Background(), tx)
	require.NoError(t, err)
	assert.Equal(t, CephStateNotBootstrapped, lc.CephBootstrapState, "cluster without legacy config must stay not_bootstrapped")
}

// TestSetClusterLifecycleDefault verifies the default lifecycle state after schema creation.
func TestGetClusterLifecycleDefault(t *testing.T) {
	db := setupLifecycleDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	lc, err := GetClusterLifecycle(context.Background(), tx)
	require.NoError(t, err)
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
	assert.Equal(t, CephStateInProgress, lc.CephBootstrapState)
	assert.Equal(t, "node-b", lc.CephBootstrapTarget)
}

// TestSetClusterLifecycleBootstrapped verifies setting the state to bootstrapped.
func TestSetClusterLifecycleBootstrapped(t *testing.T) {
	db := setupLifecycleDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	err = SetClusterLifecycle(context.Background(), tx, ClusterLifecycle{
		CephBootstrapState: CephStateBootstrapped,
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	tx2, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx2.Rollback() }()

	lc, err := GetClusterLifecycle(context.Background(), tx2)
	require.NoError(t, err)
	assert.Equal(t, CephStateBootstrapped, lc.CephBootstrapState)
}

// TestSetClusterLifecycleFailed verifies setting the state to failed with detail.
func TestSetClusterLifecycleFailed(t *testing.T) {
	db := setupLifecycleDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	err = SetClusterLifecycle(context.Background(), tx, ClusterLifecycle{
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
		CephBootstrapState: CephStateInProgress,
	})
	require.NoError(t, err)
	require.NoError(t, tx1.Commit())

	// Second update.
	tx2, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = SetClusterLifecycle(context.Background(), tx2, ClusterLifecycle{
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
	assert.Equal(t, CephStateBootstrapped, lc.CephBootstrapState)
}
