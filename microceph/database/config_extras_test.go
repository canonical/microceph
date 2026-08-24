package database

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMonHostConfigDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
CREATE TABLE core_cluster_members (
  id   INTEGER PRIMARY KEY NOT NULL,
  name TEXT NOT NULL UNIQUE
);

CREATE TABLE config (
  id    INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
  key   TEXT NOT NULL,
  value TEXT NOT NULL,
  UNIQUE(key)
);
`)
	require.NoError(t, err)

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = schemaUpdate10(context.Background(), tx)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	return db
}

func TestSchemaUpdate10DeletesRemovedMemberMonHost(t *testing.T) {
	db := setupMonHostConfigDB(t)

	_, err := db.Exec(`
INSERT INTO core_cluster_members (id, name) VALUES (1, 'node-a'), (2, 'node-b'), (3, '1');
INSERT INTO config (key, value) VALUES
  ('mon.host.node-a', '10.0.0.1'),
  ('mon.host.node-b', '10.0.0.2'),
  ('mon.host.1', '192.0.2.1');
DELETE FROM core_cluster_members WHERE name = 'node-b';
DELETE FROM core_cluster_members WHERE name = '1';
`)
	require.NoError(t, err)

	rows, err := db.Query(`SELECT key FROM config ORDER BY key`)
	require.NoError(t, err)
	defer rows.Close()

	keys := []string{}
	for rows.Next() {
		var key string
		err = rows.Scan(&key)
		require.NoError(t, err)
		keys = append(keys, key)
	}
	require.NoError(t, rows.Err())

	assert.Equal(t, []string{"mon.host.1", "mon.host.node-a"}, keys)
}

func TestSchemaUpdate10RollsBackMemberMonHostDeletion(t *testing.T) {
	db := setupMonHostConfigDB(t)

	_, err := db.Exec(`
INSERT INTO core_cluster_members (id, name) VALUES (1, 'node-a');
INSERT INTO config (key, value) VALUES ('mon.host.node-a', '10.0.0.1');
`)
	require.NoError(t, err)

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	_, err = tx.Exec(`DELETE FROM core_cluster_members WHERE name = 'node-a'`)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())

	var value string
	err = db.QueryRow(`SELECT value FROM config WHERE key = 'mon.host.node-a'`).Scan(&value)
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1", value)
}

func TestUpsertConfigItemCreatesMissingConfig(t *testing.T) {
	db := setupMonHostConfigDB(t)

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = UpsertConfigItem(context.Background(), tx, ConfigItem{Key: "mon.host.node-a", Value: "10.0.0.1"})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	var value string
	err = db.QueryRow(`SELECT value FROM config WHERE key = 'mon.host.node-a'`).Scan(&value)
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1", value)
}

func TestUpsertConfigItemReplacesStaleConfig(t *testing.T) {
	db := setupMonHostConfigDB(t)

	_, err := db.Exec(`INSERT INTO config (key, value) VALUES ('mon.host.node-a', '10.0.0.1')`)
	require.NoError(t, err)

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = UpsertConfigItem(context.Background(), tx, ConfigItem{Key: "mon.host.node-a", Value: "10.0.0.42"})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	var value string
	err = db.QueryRow(`SELECT value FROM config WHERE key = 'mon.host.node-a'`).Scan(&value)
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.42", value)

	var count int
	err = db.QueryRow(`SELECT count(*) FROM config WHERE key = 'mon.host.node-a'`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}
