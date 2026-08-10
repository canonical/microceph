package database

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupRGWFrontendDB creates an in-memory SQLite database (with foreign keys
// enabled) containing a minimal core_cluster_members table (the FK target) and
// the real schemaUpdate10 migration that creates rgw_frontends. Two named
// members are seeded so name->id resolution and cascade-delete can be tested.
func setupRGWFrontendDB(t *testing.T) *sql.DB {
	t.Helper()
	// _foreign_keys=on is per-connection in SQLite; the DSN parameter ensures
	// every pooled connection honours ON DELETE CASCADE.
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=on")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Minimal core_cluster_members matching the columns the rgw_frontends
	// migration and the name->id subqueries reference.
	_, err = db.Exec(`
CREATE TABLE core_cluster_members (
  id   INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
  name TEXT NOT NULL UNIQUE
);
INSERT INTO core_cluster_members (name) VALUES ('node-a');
INSERT INTO core_cluster_members (name) VALUES ('node-b');
`)
	require.NoError(t, err)

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = schemaUpdate10(context.Background(), tx)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	return db
}

// TestSchemaUpdate10CreatesTable verifies schemaUpdate10 creates the
// rgw_frontends table on an upgraded database.
func TestSchemaUpdate10CreatesTable(t *testing.T) {
	db := setupRGWFrontendDB(t)

	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE name = 'rgw_frontends'`).Scan(&name)
	require.NoError(t, err)
	assert.Equal(t, "rgw_frontends", name)
}

// TestUpsertRGWFrontendAndGetAll verifies an upsert is persisted and read back
// keyed by member name, with ports and the TLS flag intact.
func TestUpsertRGWFrontendAndGetAll(t *testing.T) {
	db := setupRGWFrontendDB(t)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	err = UpsertRGWFrontend(ctx, tx, "node-a", 80, 0, false)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	tx2, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx2.Rollback() }()

	frontends, err := GetRGWFrontends(ctx, tx2)
	require.NoError(t, err)
	require.Len(t, frontends, 1)
	assert.Equal(t, "node-a", frontends[0].Member)
	assert.Equal(t, 80, frontends[0].Port)
	assert.Equal(t, 0, frontends[0].SSLPort)
	assert.False(t, frontends[0].SSL)
}

// TestUpsertRGWFrontendOverwrites verifies a second upsert for the same member
// replaces the row (cert rotation / port change), not a duplicate.
func TestUpsertRGWFrontendOverwrites(t *testing.T) {
	db := setupRGWFrontendDB(t)
	ctx := context.Background()

	lockTx(t, db, func(tx *sql.Tx) {
		require.NoError(t, UpsertRGWFrontend(ctx, tx, "node-a", 80, 0, false))
	})
	lockTx(t, db, func(tx *sql.Tx) {
		require.NoError(t, UpsertRGWFrontend(ctx, tx, "node-a", 8080, 443, true))
	})

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	frontends, err := GetRGWFrontends(ctx, tx)
	require.NoError(t, err)
	require.Len(t, frontends, 1, "upsert must replace, not append")
	assert.Equal(t, 8080, frontends[0].Port)
	assert.Equal(t, 443, frontends[0].SSLPort)
	assert.True(t, frontends[0].SSL)
}

// TestGetRGWFrontendsMultipleMembers verifies the read returns each member's
// frontend keyed by name.
func TestGetRGWFrontendsMultipleMembers(t *testing.T) {
	db := setupRGWFrontendDB(t)
	ctx := context.Background()

	lockTx(t, db, func(tx *sql.Tx) {
		require.NoError(t, UpsertRGWFrontend(ctx, tx, "node-a", 80, 0, false))
		require.NoError(t, UpsertRGWFrontend(ctx, tx, "node-b", 8443, 443, true))
	})

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	frontends, err := GetRGWFrontends(ctx, tx)
	require.NoError(t, err)
	require.Len(t, frontends, 2)

	byName := map[string]RgwFrontend{}
	for _, f := range frontends {
		byName[f.Member] = f
	}
	assert.Contains(t, byName, "node-a")
	assert.Contains(t, byName, "node-b")
	assert.True(t, byName["node-b"].SSL)
}

// TestDeleteRGWFrontendByMember verifies the row is removed and that deleting a
// missing row is not an error (idempotent scale-down / retry).
func TestDeleteRGWFrontendByMember(t *testing.T) {
	db := setupRGWFrontendDB(t)
	ctx := context.Background()

	lockTx(t, db, func(tx *sql.Tx) {
		require.NoError(t, UpsertRGWFrontend(ctx, tx, "node-a", 80, 0, false))
	})

	lockTx(t, db, func(tx *sql.Tx) {
		require.NoError(t, DeleteRGWFrontendByMember(ctx, tx, "node-a"))
	})

	// Read in its own (rolled-back) transaction, released before the re-delete
	// below so the pool reuses the same :memory: connection.
	func() {
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		frontends, err := GetRGWFrontends(ctx, tx)
		require.NoError(t, err)
		assert.Empty(t, frontends)
		require.NoError(t, tx.Rollback())
	}()

	// Deleting an already-absent row must succeed (idempotent).
	lockTx(t, db, func(tx *sql.Tx) {
		require.NoError(t, DeleteRGWFrontendByMember(ctx, tx, "node-a"))
	})
}

// TestRGWFrontendCascadeOnMemberRemoval verifies the ON DELETE CASCADE foreign
// key removes the frontend row when the cluster member is deleted.
func TestRGWFrontendCascadeOnMemberRemoval(t *testing.T) {
	db := setupRGWFrontendDB(t)
	ctx := context.Background()

	lockTx(t, db, func(tx *sql.Tx) {
		require.NoError(t, UpsertRGWFrontend(ctx, tx, "node-a", 80, 0, false))
	})

	_, err := db.Exec(`DELETE FROM core_cluster_members WHERE name = 'node-a'`)
	require.NoError(t, err)

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	frontends, err := GetRGWFrontends(ctx, tx)
	require.NoError(t, err)
	assert.Empty(t, frontends, "removing the member must cascade-delete its frontend row")
}
