package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	cluster "github.com/canonical/microcluster/v3/microcluster/db"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupDisksDB creates an in-memory SQLite database with the current-shape
// core_cluster_members and disks tables, runs the real schemaUpdate13
// migration adding the removing column, and prepares the registered
// statements whose tables exist (skipErrors leaves the rest unprepared).
func setupDisksDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=on")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
CREATE TABLE core_cluster_members (
  id   INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
  name TEXT NOT NULL
);
CREATE TABLE disks (
  id        INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
  member_id INTEGER NOT NULL,
  path      TEXT NOT NULL,
  FOREIGN KEY (member_id) REFERENCES "core_cluster_members" (id) ON DELETE CASCADE,
  UNIQUE(member_id, path)
);
INSERT INTO core_cluster_members (id, name) VALUES (1, 'node-a'), (2, 'node-b'), (3, 'node-c');
INSERT INTO disks (id, member_id, path) VALUES (0, 1, '/dev/sda'), (1, 2, '/dev/sda'), (2, 3, '/dev/sda');
`)
	require.NoError(t, err)

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = schemaUpdate13(context.Background(), tx)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	err = cluster.PrepareStmts(db, true)
	require.NoError(t, err)

	return db
}

// TestSchemaUpdate13DefaultsExistingRows verifies the migration adds the
// removing_since column and leaves existing rows eligible (removing_since = 0).
func TestSchemaUpdate13DefaultsExistingRows(t *testing.T) {
	db := setupDisksDB(t)

	var count int
	err := db.QueryRow(`SELECT count(*) FROM disks WHERE removing_since = 0`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

// TestMembersDiskCntExcludesRemoving is the regression test for the
// removal/add race: an OSD whose removal is in flight must not count as a
// remaining host, or a concurrent disk add would switch pools back to the
// wider failure domain the scale-down just left.
func TestMembersDiskCntExcludesRemoving(t *testing.T) {
	db := setupDisksDB(t)
	ctx := context.Background()

	count := func(exclude int64) int {
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		records, err := MembersDiskCnt(ctx, tx, exclude)
		require.NoError(t, err)
		return len(records)
	}

	assert.Equal(t, 3, count(-1), "all three hosts have disks")

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	err = MarkDiskRemoving(ctx, tx, 2)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	var since int64
	err = db.QueryRow(`SELECT removing_since FROM disks WHERE id = 2`).Scan(&since)
	require.NoError(t, err)
	assert.NotZero(t, since, "the mark must record the marking time")

	assert.Equal(t, 2, count(-1), "the removing OSD's host must not be counted")
	assert.Equal(t, 2, count(2), "flag and exclude parameter overlap on the same OSD")
	assert.Equal(t, 1, count(1), "another removal excludes the flag OSD too: 3 hosts minus osd.1 minus removing osd.2")

	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	err = ClearDiskRemoving(ctx, tx, 2)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	assert.Equal(t, 3, count(-1), "a verified rollback restores eligibility")
}

// TestMarkDiskRemovingNoRow verifies marking a nonexistent OSD fails loudly:
// the removal flow relies on the mark to keep concurrent counts honest.
func TestMarkDiskRemovingNoRow(t *testing.T) {
	db := setupDisksDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	err = MarkDiskRemoving(context.Background(), tx, 999)
	assert.Error(t, err)
}

// TestClearDiskRemovingNoRow verifies clearing is idempotent: the row may
// already be deleted when a rolled-back removal cleans up.
func TestClearDiskRemovingNoRow(t *testing.T) {
	db := setupDisksDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	err = ClearDiskRemoving(context.Background(), tx, 999)
	assert.NoError(t, err)
}

// TestListRemovingDisks verifies the stale-flag listing is scoped to the
// given member and to flags older than the given time, so the startup
// reconciliation never touches flags of removals in flight on any member.
func TestListRemovingDisks(t *testing.T) {
	db := setupDisksDB(t)
	ctx := context.Background()

	before := time.Now().UnixNano()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	err = MarkDiskRemoving(ctx, tx, 0)
	require.NoError(t, err)
	err = MarkDiskRemoving(ctx, tx, 2)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	after := time.Now().UnixNano()
	list := func(member string, markedBefore int64) []int64 {
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		osds, err := ListRemovingDisks(ctx, tx, member, markedBefore)
		require.NoError(t, err)
		return osds
	}

	assert.ElementsMatch(t, []int64{0}, list("node-a", after), "the member's own stale flag is listed")
	assert.ElementsMatch(t, []int64{2}, list("node-c", after), "flags are scoped per member")
	assert.Empty(t, list("node-b", after))
	assert.Empty(t, list("node-a", before), "a flag newer than the cutoff is an in-flight removal, never stale")
}
