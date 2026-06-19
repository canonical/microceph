package database

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupPlacementDB creates an in-memory SQLite database with the placement_policy
// table and seeds the singleton row.
func setupPlacementDB(t *testing.T) *sql.DB {
	t.Helper()
	db := setupLifecycleDB(t) // reuse the sqlite opener
	_, err := db.Exec(`
CREATE TABLE placement_policy (
  id          INTEGER PRIMARY KEY NOT NULL DEFAULT 1,
  active      INTEGER NOT NULL DEFAULT 0,
  policy_json TEXT,
  last_refusal TEXT DEFAULT NULL,
  CONSTRAINT singleton CHECK (id = 1)
);
INSERT INTO placement_policy (id) VALUES (1);
`)
	require.NoError(t, err)
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
