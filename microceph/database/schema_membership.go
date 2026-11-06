package database

import (
	"context"
	"database/sql"
	"fmt"
)

// schemaUpdate4 repairs application foreign keys after Microcluster's core-table rename.
func schemaUpdate4(ctx context.Context, tx *sql.Tx) error {
	// Internal schema updates run with foreign_keys=OFF and legacy_alter_table=ON.
	// Consequently their rename does not rewrite existing application foreign keys.
	// Rebuild only affected tables, inside the schema transaction, retaining IDs,
	// AUTOINCREMENT high-water marks, and the global-config uniqueness index.
	tables := []struct {
		name       string
		columns    string
		definition string
		index      string
	}{
		{
			name: "disks", columns: "id, member_id, path",
			definition: `
  id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
  member_id INTEGER NOT NULL,
  path TEXT NOT NULL,
  FOREIGN KEY (member_id) REFERENCES core_cluster_members (id) ON DELETE CASCADE,
  UNIQUE(member_id, path)`,
		},
		{
			name: "services", columns: "id, member_id, service",
			definition: `
  id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
  member_id INTEGER NOT NULL,
  service TEXT NOT NULL,
  FOREIGN KEY (member_id) REFERENCES core_cluster_members (id) ON DELETE CASCADE,
  UNIQUE(member_id, service)`,
		},
		{
			name: "client_config", columns: "id, member_id, key, value",
			definition: `
  id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
  member_id INTEGER,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  FOREIGN KEY (member_id) REFERENCES core_cluster_members (id) ON DELETE CASCADE`,
			index: `CREATE UNIQUE INDEX cc_index ON client_config(coalesce(member_id, 0), key);`,
		},
	}

	for _, table := range tables {
		var legacy int
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_foreign_key_list(?) WHERE "table"='internal_cluster_members'`, table.name).Scan(&legacy)
		if err != nil {
			return fmt.Errorf("inspect %s foreign keys: %w", table.name, err)
		}
		if legacy == 0 {
			continue
		}

		// Identifiers and definitions come exclusively from the fixed list above.
		stmt := fmt.Sprintf(`
ALTER TABLE %[1]s RENAME TO %[1]s_legacy;
CREATE TABLE %[1]s (%[2]s);
INSERT INTO %[1]s (%[3]s) SELECT %[3]s FROM %[1]s_legacy;
UPDATE sqlite_sequence
SET seq = MAX(seq, COALESCE((SELECT seq FROM sqlite_sequence WHERE name='%[1]s_legacy'), 0))
WHERE name='%[1]s';
DROP TABLE %[1]s_legacy;
%[4]s`, table.name, table.definition, table.columns, table.index)
		_, err = tx.ExecContext(ctx, stmt)
		if err != nil {
			return fmt.Errorf("repair %s foreign keys: %w", table.name, err)
		}
	}

	return nil
}
