// Package database provides the database access functions and schema.
package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/db/schema"
)

// SchemaExtensions is a list of schema extensions that can be passed to the MicroCluster daemon.
// Each entry will increase the database schema version by one, and will be applied after internal schema updates.
var SchemaExtensions = []schema.Update{
	schemaUpdate1,
	schemaUpdate2,
	schemaUpdate3,
	schemaUpdate4,
}

// getClusterTableName returns the name of the table that holds the record of cluster members from sqlite_master.
// Prior to microcluster V2, this table was called `internal_cluster_members`, but now it is `core_cluster_members`.
// Since extensions to the database may be at an earlier version (either 1, 2, or 3), this helper will dynamically determine the table name to use.
func getClusterTableName(ctx context.Context, tx *sql.Tx) (string, error) {
	stmt := "SELECT name FROM sqlite_master WHERE name = 'internal_cluster_members' OR name = 'core_cluster_members'"
	tables, err := query.SelectStrings(ctx, tx, stmt)
	if err != nil {
		return "", err
	}

	if len(tables) != 1 || tables[0] == "" {
		return "", fmt.Errorf("No cluster members table found")
	}

	return tables[0], nil
}

func schemaUpdate1(ctx context.Context, tx *sql.Tx) error {
	tableName, err := getClusterTableName(ctx, tx)
	if err != nil {
		return err
	}

	stmt := fmt.Sprintf(`
CREATE TABLE config (
  id                            INTEGER  PRIMARY KEY AUTOINCREMENT NOT NULL,
  key                           TEXT     NOT  NULL,
  value                         TEXT     NOT  NULL,
  UNIQUE(key)
);

CREATE TABLE disks (
  id                            INTEGER  PRIMARY KEY AUTOINCREMENT NOT NULL,
  member_id                     INTEGER  NOT  NULL,
  path                          TEXT     NOT  NULL,
  osd                           INTEGER  NOT  NULL,
  FOREIGN KEY (member_id) REFERENCES "%s" (id) ON DELETE CASCADE,
  UNIQUE(member_id, path),
  UNIQUE(osd)
);

CREATE TABLE services (
  id                            INTEGER  PRIMARY KEY AUTOINCREMENT NOT NULL,
  member_id                     INTEGER  NOT  NULL,
  service                       TEXT     NOT  NULL,
  FOREIGN KEY (member_id) REFERENCES "%s" (id) ON DELETE CASCADE,
  UNIQUE(member_id, service)
);
  `, tableName, tableName)

	_, err = tx.Exec(stmt)

	return err
}

// Adds client config table in database schema.
func schemaUpdate2(ctx context.Context, tx *sql.Tx) error {
	tableName, err := getClusterTableName(ctx, tx)
	if err != nil {
		return err
	}

	stmt := fmt.Sprintf(`
CREATE TABLE client_config (
  id                            INTEGER  PRIMARY KEY AUTOINCREMENT NOT NULL,
  member_id                     INTEGER,
  key                           TEXT     NOT  NULL,
  value                         TEXT     NOT  NULL,
  FOREIGN KEY (member_id) REFERENCES "%s" (id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX cc_index ON client_config(coalesce(member_id, 0), key);
  `, tableName)

	_, err = tx.Exec(stmt)

	return err
}

// schemaUpdate3 generates the diskpaths table, copying the data from the disks table.
func schemaUpdate3(ctx context.Context, tx *sql.Tx) error {
	tableName, err := getClusterTableName(ctx, tx)
	if err != nil {
		return err
	}

	stmt := fmt.Sprintf(`
CREATE TABLE disks2 (
  id                            INTEGER  PRIMARY KEY AUTOINCREMENT NOT NULL,
  member_id                     INTEGER  NOT  NULL,
  path                          TEXT     NOT  NULL,
  FOREIGN KEY (member_id) REFERENCES "%s" (id) ON DELETE CASCADE,
  UNIQUE(member_id, path)
);
INSERT INTO disks2 (id, member_id, path)
SELECT osd, member_id, path FROM disks;
DROP TABLE disks;
ALTER TABLE disks2 RENAME TO disks;
  `, tableName)
	_, err = tx.Exec(stmt)

	return err
}

// schemaUpdate4 updates all tables referencing `internal_cluster_members` to now reference `core_cluster_members` instead.
func schemaUpdate4(ctx context.Context, tx *sql.Tx) error {
	stmt := `
CREATE TABLE disks2 (
  id                            INTEGER  PRIMARY KEY AUTOINCREMENT NOT NULL,
  member_id                     INTEGER  NOT  NULL,
  path                          TEXT     NOT  NULL,
  FOREIGN KEY (member_id) REFERENCES "core_cluster_members" (id) ON DELETE CASCADE,
  UNIQUE(member_id, path)
);
INSERT INTO disks2 (id, member_id, path)
SELECT id, member_id, path FROM disks;
DROP TABLE disks;
ALTER TABLE disks2 RENAME TO disks;


DROP INDEX IF EXISTS cc_index;
CREATE TABLE client_config_new (
  id                            INTEGER  PRIMARY KEY AUTOINCREMENT NOT NULL,
  member_id                     INTEGER,
  key                           TEXT     NOT  NULL,
  value                         TEXT     NOT  NULL,
  FOREIGN KEY (member_id) REFERENCES "core_cluster_members" (id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX cc_index ON client_config(coalesce(member_id, 0), key);
INSERT INTO client_config_new (id,member_id,key,value) SELECT id,member_id,key,value FROM client_config;
DROP TABLE client_config;
ALTER TABLE client_config_new RENAME TO client_config;


CREATE TABLE services_new (
  id                            INTEGER  PRIMARY KEY AUTOINCREMENT NOT NULL,
  member_id                     INTEGER  NOT  NULL,
  service                       TEXT     NOT  NULL,
  FOREIGN KEY (member_id) REFERENCES "core_cluster_members" (id) ON DELETE CASCADE,
  UNIQUE(member_id, service)
);

INSERT INTO services_new (id,member_id,service) SELECT id,member_id,service FROM services;
DROP TABLE services;
ALTER TABLE services_new RENAME TO services;
  `
	_, err := tx.ExecContext(ctx, stmt)

	return err
}

// schemaUpdate5 adds remote tables
func schemaUpdate5(ctx context.Context, tx *sql.Tx) error {
	stmt := `
CREATE TABLE remote (
  id                            INTEGER  PRIMARY KEY AUTOINCREMENT NOT NULL,
  name                          TEXT     NOT  NULL,
  local_name                    TEXT     NOT  NULL,
  UNIQUE(name)
);

CREATE TABLE remote_config (
  id                            INTEGER  PRIMARY KEY AUTOINCREMENT NOT NULL,
  remote_id                     INT      NOT  NULL,
  key                           TEXT     NOT  NULL,
  value                         TEXT     NOT  NULL,
  FOREIGN KEY (remote_id) REFERENCES "remote" (id) ON DELETE CASCADE,
  UNIQUE(remote_id, key)
);
  `
	_, err := tx.Exec(stmt)

	return err
}
