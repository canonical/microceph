package database

import (
	"context"
	"database/sql"
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
