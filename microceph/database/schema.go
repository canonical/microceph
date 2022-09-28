// Package database provides the database access functions and schema.
package database

import (
	"context"
	"database/sql"

	"github.com/lxc/lxd/lxd/db/schema"
)

// SchemaExtensions is a list of schema extensions that can be passed to the MicroCluster daemon.
// Each entry will increase the database schema version by one, and will be applied after internal schema updates.
var SchemaExtensions = map[int]schema.Update{
	1: schemaUpdate1,
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
  FOREIGN KEY (member_id) REFERENCES "internal_cluster_members" (id)
  UNIQUE(member_id, path),
  UNIQUE(osd)
);

CREATE TABLE services (
  id                            INTEGER  PRIMARY KEY AUTOINCREMENT NOT NULL,
  member_id                     INTEGER  NOT  NULL,
  service                       TEXT     NOT  NULL,
  FOREIGN KEY (member_id) REFERENCES "internal_cluster_members" (id)
  UNIQUE(member_id, service)
);
  `

	_, err := tx.Exec(stmt)

	return err
}
