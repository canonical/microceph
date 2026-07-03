package database

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupPlacementDB creates an in-memory SQLite database with the
// placement_policy table by running the real schemaUpdate9 migration. This
// exercises the exact SQL that runs on a deployed cluster's upgrade, including
// the last_refusal and apply_lock_token columns.
func setupPlacementDB(t *testing.T) *sql.DB {
	t.Helper()
	db := setupLifecycleDB(t) // creates config table + cluster_lifecycle via schemaUpdate8

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = schemaUpdate9(context.Background(), tx)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	return db
}

// TestGetPlacementPolicyDefault verifies the default placement policy is inactive with empty JSON.
func TestGetPlacementPolicyDefault(t *testing.T) {
	db := setupPlacementDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	rec, err := GetPlacementPolicy(context.Background(), tx)
	require.NoError(t, err)
	assert.False(t, rec.Active)
	assert.Empty(t, rec.PolicyJSON)
}

// TestSetPlacementPolicyActive verifies setting an active policy with JSON.
func TestSetPlacementPolicyActive(t *testing.T) {
	db := setupPlacementDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	err = SetPlacementPolicy(context.Background(), tx, true, `{"members":{"node-a":{"control":true}}}`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	tx2, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx2.Rollback() }()

	rec, err := GetPlacementPolicy(context.Background(), tx2)
	require.NoError(t, err)
	assert.True(t, rec.Active)
	assert.Contains(t, rec.PolicyJSON, "node-a")
}

// TestClearPlacementPolicy verifies clearing the policy sets Active to false.
func TestClearPlacementPolicy(t *testing.T) {
	db := setupPlacementDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = SetPlacementPolicy(context.Background(), tx, true, `{"members":{}}`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	tx2, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = ClearPlacementPolicy(context.Background(), tx2)
	require.NoError(t, err)
	require.NoError(t, tx2.Commit())

	tx3, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx3.Rollback() }()

	rec, err := GetPlacementPolicy(context.Background(), tx3)
	require.NoError(t, err)
	assert.False(t, rec.Active)
}

// lockTx runs fn inside a committed transaction on db.
func lockTx(t *testing.T, db *sql.DB, fn func(tx *sql.Tx)) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	fn(tx)
	require.NoError(t, tx.Commit())
}

// TestPlacementApplyLockAcquireAndContention verifies that a free lock is
// acquired and that a second acquirer is refused while the first holder's
// lease is live.
func TestPlacementApplyLockAcquireAndContention(t *testing.T) {
	db := setupPlacementDB(t)
	ctx := context.Background()

	const lease = int64(1000)
	first := int64(5000)
	second := int64(5100)

	lockTx(t, db, func(tx *sql.Tx) {
		acquired, err := TryAcquirePlacementApplyLock(ctx, tx, first, first-lease)
		require.NoError(t, err)
		assert.True(t, acquired, "a free lock must be acquired")
	})

	lockTx(t, db, func(tx *sql.Tx) {
		acquired, err := TryAcquirePlacementApplyLock(ctx, tx, second, second-lease)
		require.NoError(t, err)
		assert.False(t, acquired, "a live holder must block a second acquirer")
	})
}

// TestPlacementApplyLockStaleReclaim verifies that a lock whose holder's
// lease has expired is reclaimed by the next acquirer.
func TestPlacementApplyLockStaleReclaim(t *testing.T) {
	db := setupPlacementDB(t)
	ctx := context.Background()

	const lease = int64(1000)
	stale := int64(5000)
	// The reclaimer arrives after the stale holder's lease expired.
	reclaimer := stale + lease + 1

	lockTx(t, db, func(tx *sql.Tx) {
		acquired, err := TryAcquirePlacementApplyLock(ctx, tx, stale, stale-lease)
		require.NoError(t, err)
		require.True(t, acquired)
	})

	lockTx(t, db, func(tx *sql.Tx) {
		acquired, err := TryAcquirePlacementApplyLock(ctx, tx, reclaimer, reclaimer-lease)
		require.NoError(t, err)
		assert.True(t, acquired, "an expired lease must be reclaimable")
	})
}

// TestPlacementApplyLockReleaseOnlyOwnToken verifies that release is
// conditional on the holder's token: a stale holder cannot clobber a
// reclaimer's lock, and a proper release frees the lock for the next acquirer.
func TestPlacementApplyLockReleaseOnlyOwnToken(t *testing.T) {
	db := setupPlacementDB(t)
	ctx := context.Background()

	const lease = int64(1000)
	holder := int64(5000)
	stranger := int64(4000)

	lockTx(t, db, func(tx *sql.Tx) {
		acquired, err := TryAcquirePlacementApplyLock(ctx, tx, holder, holder-lease)
		require.NoError(t, err)
		require.True(t, acquired)
	})

	lockTx(t, db, func(tx *sql.Tx) {
		released, err := ReleasePlacementApplyLock(ctx, tx, stranger)
		require.NoError(t, err)
		assert.False(t, released, "release with a foreign token must not free the lock")
	})

	lockTx(t, db, func(tx *sql.Tx) {
		released, err := ReleasePlacementApplyLock(ctx, tx, holder)
		require.NoError(t, err)
		assert.True(t, released, "release with the holder's token must free the lock")
	})

	next := int64(5200)
	lockTx(t, db, func(tx *sql.Tx) {
		acquired, err := TryAcquirePlacementApplyLock(ctx, tx, next, next-lease)
		require.NoError(t, err)
		assert.True(t, acquired, "a released lock must be acquirable again")
	})
}
