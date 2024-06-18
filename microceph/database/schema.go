// Package database provides the database access functions and schema.
package database

import (
	"context"
	"database/sql"

	"github.com/canonical/lxd/lxd/db/schema"
)

// SchemaExtensions is a list of schema extensions that can be passed to the MicroCluster daemon.
// Each entry will increase the database schema version by one, and will be applied after internal schema updates.
var SchemaExtensions = []schema.Update{
	schemaUpdate1,
	schemaUpdate2,
	schemaUpdate3,
}

func schemaUpdate1(ctx context.Context, tx *sql.Tx) error {
	stmt := `
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
  FOREIGN KEY (member_id) REFERENCES "internal_cluster_members" (id) ON DELETE CASCADE,
  UNIQUE(member_id, path),
  UNIQUE(osd)
);

CREATE TABLE services (
  id                            INTEGER  PRIMARY KEY AUTOINCREMENT NOT NULL,
  member_id                     INTEGER  NOT  NULL,
  service                       TEXT     NOT  NULL,
  FOREIGN KEY (member_id) REFERENCES "internal_cluster_members" (id) ON DELETE CASCADE,
  UNIQUE(member_id, service)
);
  `

	_, err := tx.Exec(stmt)

	return err
}

// Adds client config table in database schema.
func schemaUpdate2(ctx context.Context, tx *sql.Tx) error {
	stmt := `
CREATE TABLE client_config (
  id                            INTEGER  PRIMARY KEY AUTOINCREMENT NOT NULL,
  member_id                     INTEGER,
  key                           TEXT     NOT  NULL,
  value                         TEXT     NOT  NULL,
  FOREIGN KEY (member_id) REFERENCES "internal_cluster_members" (id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX cc_index ON client_config(coalesce(member_id, 0), key);
  `

	_, err := tx.Exec(stmt)

	return err
}

// schemaUpdate3 generates the diskpaths table, copying the data from the disks table.
func schemaUpdate3(ctx context.Context, tx *sql.Tx) error {
	stmt := `
CREATE TABLE disks2 (
  id                            INTEGER  PRIMARY KEY AUTOINCREMENT NOT NULL,
  member_id                     INTEGER  NOT  NULL,
  path                          TEXT     NOT  NULL,
  FOREIGN KEY (member_id) REFERENCES "internal_cluster_members" (id) ON DELETE CASCADE,
  UNIQUE(member_id, path)
);
INSERT INTO disks2 (id, member_id, path)
SELECT osd, member_id, path FROM disks;
DROP TABLE disks;
ALTER TABLE disks2 RENAME TO disks;
  `
	_, err := tx.Exec(stmt)

	return err
}
