package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func setupGroupedServiceDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
CREATE TABLE core_cluster_members (
  id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
  name TEXT NOT NULL UNIQUE
);
INSERT INTO core_cluster_members (name) VALUES ('node-a');
`)
	require.NoError(t, err)

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = schemaUpdate6(context.Background(), tx)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	return db
}

func TestSMBGroupStateJSONRoundTrip(t *testing.T) {
	rank := 0
	config := SMBServiceGroupConfig{CTDBRanks: map[string]int{"smb.files.node-a": rank}, NextCTDBRank: 1}
	info := SMBServiceInfo{ConfigURI: "rados://.smb/files/config.smb", CTDBRank: &rank,
		CTDBIdentity: "smb.files.node-a", AppliedSpec: []byte(`{"service_id":"files"}`), ConfigDigest: "sha256:abc"}
	configJSON, err := json.Marshal(config)
	require.NoError(t, err)
	require.JSONEq(t, `{"desired_spec":null,"ctdb_ranks":{"smb.files.node-a":0},"next_ctdb_rank":1}`, string(configJSON))
	infoJSON, err := json.Marshal(info)
	require.NoError(t, err)
	require.JSONEq(t, `{"config_uri":"rados://.smb/files/config.smb","ctdb_rank":0,"ctdb_identity":"smb.files.node-a","applied_spec":{"service_id":"files"},"config_digest":"sha256:abc"}`, string(infoJSON))
	var restored SMBServiceInfo
	require.NoError(t, json.Unmarshal(infoJSON, &restored))
	require.Equal(t, info, restored)
}

func TestGetGroupedServicesWithGroupConfigReturnsSharedConfiguration(t *testing.T) {
	db := setupGroupedServiceDB(t)
	_, err := db.Exec(`
INSERT INTO service_groups (service, group_id, config)
VALUES ('smb', 'files', '{"desired_spec":{"service_id":"files"}}');
INSERT INTO grouped_services (service_group_id, member_id, info)
VALUES (
  (SELECT id FROM service_groups WHERE service = 'smb' AND group_id = 'files'),
  (SELECT id FROM core_cluster_members WHERE name = 'node-a'),
  '{"config_uri":"rados://.smb/files/config.smb"}'
);
`)
	require.NoError(t, err)

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	records, err := getGroupedServicesWithGroupConfig(context.Background(), tx)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())

	require.Equal(t, []GroupedServiceWithGroupConfig{
		{
			GroupedService: GroupedService{
				ID:      1,
				Service: "smb",
				GroupID: "files",
				Member:  "node-a",
				Info:    `{"config_uri":"rados://.smb/files/config.smb"}`,
			},
			GroupConfig: `{"desired_spec":{"service_id":"files"}}`,
		},
	}, records)
}

func TestAddOrUpdateSMBPreservesRanksAcrossStaleMemberUpdates(t *testing.T) {
	db := setupGroupedServiceDB(t)
	_, err := db.Exec(`INSERT INTO core_cluster_members (name) VALUES ('node-b'), ('node-c')`)
	require.NoError(t, err)
	ctx := context.Background()
	update := func(member, config, info string) error {
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		err = addOrUpdateGroupedService(ctx, tx, member, "smb", "files", config, info)
		if err != nil {
			require.NoError(t, tx.Rollback())
			return err
		}
		return tx.Commit()
	}
	first := `{"desired_spec":{"revision":1},"ctdb_ranks":{"smb.files.node-a":0},"next_ctdb_rank":1}`
	require.NoError(t, update("node-a", first, `{"config_uri":"a","ctdb_rank":0}`))
	second := `{"desired_spec":{"revision":2},"ctdb_ranks":{"smb.files.node-a":0,"smb.files.node-b":1},"next_ctdb_rank":2}`
	require.NoError(t, update("node-b", second, `{"config_uri":"b","ctdb_rank":1}`))
	// A stale local agent must not erase the newly allocated rank or counter.
	require.NoError(t, update("node-a", first, `{"config_uri":"a","ctdb_rank":0}`))
	var stored string
	err = db.QueryRow(`SELECT config FROM service_groups WHERE group_id = 'files'`).Scan(&stored)
	require.NoError(t, err)
	require.JSONEq(t, `{"desired_spec":{"revision":1},"ctdb_ranks":{"smb.files.node-a":0,"smb.files.node-b":1},"next_ctdb_rank":2}`, stored)

	// Removal of a member record does not discard its rank from shared state.
	_, err = db.Exec(`DELETE FROM grouped_services WHERE member_id = (SELECT id FROM core_cluster_members WHERE name = 'node-a')`)
	require.NoError(t, err)
	third := `{"desired_spec":{"revision":3},"ctdb_ranks":{"smb.files.node-b":1,"smb.files.node-c":2},"next_ctdb_rank":3}`
	require.NoError(t, update("node-c", third, `{"config_uri":"c","ctdb_rank":2}`))
	err = db.QueryRow(`SELECT config FROM service_groups WHERE group_id = 'files'`).Scan(&stored)
	require.NoError(t, err)
	require.JSONEq(t, `{"desired_spec":{"revision":3},"ctdb_ranks":{"smb.files.node-a":0,"smb.files.node-b":1,"smb.files.node-c":2},"next_ctdb_rank":3}`, stored)
	require.NoError(t, update("node-a", first, `{"config_uri":"a","ctdb_rank":0}`))
	// Neither a different identity nor a stale counter can recycle an old rank.
	bad := `{"desired_spec":{},"ctdb_ranks":{"smb.files.node-d":0},"next_ctdb_rank":1}`
	require.ErrorContains(t, update("node-a", bad, `{}`), "retired")
	bad = `{"desired_spec":{},"ctdb_ranks":{"smb.files.node-d":3},"next_ctdb_rank":4}`
	require.NoError(t, update("node-a", bad, `{}`))
	err = db.QueryRow(`SELECT config FROM service_groups WHERE group_id = 'files'`).Scan(&stored)
	require.NoError(t, err)
	require.JSONEq(t, `{"desired_spec":{},"ctdb_ranks":{"smb.files.node-a":0,"smb.files.node-b":1,"smb.files.node-c":2,"smb.files.node-d":3},"next_ctdb_rank":4}`, stored)
}

func TestAddOrUpdateSMBRejectsRetiredRankAfterPartialCounter(t *testing.T) {
	db := setupGroupedServiceDB(t)
	_, err := db.Exec(`INSERT INTO service_groups (service, group_id, config) VALUES ('smb', 'files', '{"desired_spec":{},"next_ctdb_rank":4}')`)
	require.NoError(t, err)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = addOrUpdateGroupedService(context.Background(), tx, "node-a", "smb", "files", `{"desired_spec":{},"ctdb_ranks":{"smb.files.node-a":2}}`, `{}`)
	require.ErrorContains(t, err, "retired")
	require.NoError(t, tx.Rollback())
}

func TestAddOrUpdateGroupedServiceUpdatesSharedConfigAndMemberInfo(t *testing.T) {
	db := setupGroupedServiceDB(t)
	_, err := db.Exec(`
INSERT INTO service_groups (service, group_id, config)
VALUES ('smb', 'files', '{"desired_spec":{"revision":1}}');
INSERT INTO grouped_services (service_group_id, member_id, info)
VALUES (
  (SELECT id FROM service_groups WHERE service = 'smb' AND group_id = 'files'),
  (SELECT id FROM core_cluster_members WHERE name = 'node-a'),
  '{"config_uri":"old"}'
);
`)
	require.NoError(t, err)

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = addOrUpdateGroupedService(
		context.Background(),
		tx,
		"node-a",
		"smb",
		"files",
		`{"desired_spec":{"revision":2}}`,
		`{"config_uri":"rados://.smb/files/config.smb"}`,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	var groupConfig string
	var memberInfo string
	err = db.QueryRow(`
SELECT service_groups.config, grouped_services.info
  FROM grouped_services
  JOIN service_groups ON service_groups.id = grouped_services.service_group_id
 WHERE service_groups.service = 'smb'
   AND service_groups.group_id = 'files'
`).Scan(&groupConfig, &memberInfo)
	require.NoError(t, err)
	require.JSONEq(t, `{"desired_spec":{"revision":2}}`, groupConfig)
	require.JSONEq(t, `{"config_uri":"rados://.smb/files/config.smb"}`, memberInfo)
}
