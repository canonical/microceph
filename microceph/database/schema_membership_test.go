package database

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestRepairLegacyMembershipForeignKeys(t *testing.T) {
	for _, emptyDisks := range []bool{false, true} {
		t.Run(map[bool]string{false: "populated", true: "deleted IDs"}[emptyDisks], func(t *testing.T) {
			db, err := sql.Open("sqlite3", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			_, err = db.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE core_cluster_members (id INTEGER PRIMARY KEY, name TEXT UNIQUE); INSERT INTO core_cluster_members VALUES (1, 'member');`)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, update := range SchemaExtensions[:3] {
				err = update(ctx, tx)
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = tx.Exec(`
INSERT INTO config (id, key, value) VALUES (21, 'setting', 'preserved');
INSERT INTO disks (id, member_id, path) VALUES (42, 1, '/dev/test'), (100, 1, '/dev/deleted');
DELETE FROM disks WHERE id=100;
INSERT INTO services (id, member_id, service) VALUES (11, 1, 'mon');
INSERT INTO client_config (id, member_id, key, value) VALUES (12, 1, 'local-key', 'local-value'), (13, NULL, 'global-key', 'global-value');`)
			if err != nil {
				t.Fatal(err)
			}
			if emptyDisks {
				_, err = tx.Exec(`DELETE FROM disks`)
				if err != nil {
					t.Fatal(err)
				}
			}
			err = tx.Commit()
			if err != nil {
				t.Fatal(err)
			}

			// First reconstruct the old foreign-key names, then reproduce the
			// native updateFromV4 rename with its actual PRAGMA settings.
			_, err = db.Exec(`
ALTER TABLE core_cluster_members RENAME TO internal_cluster_members;
PRAGMA foreign_keys=OFF;
PRAGMA legacy_alter_table=ON;
ALTER TABLE internal_cluster_members RENAME TO core_cluster_members;
PRAGMA foreign_keys=ON;
PRAGMA legacy_alter_table=OFF;`)
			if err != nil {
				t.Fatal(err)
			}
			tx, err = db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			err = schemaUpdate4(ctx, tx)
			if err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"disks", "services", "client_config"} {
				var target string
				err = tx.QueryRow(`SELECT "table" FROM pragma_foreign_key_list(?)`, table).Scan(&target)
				if err != nil || target != "core_cluster_members" {
					t.Fatalf("%s foreign key still targets %q: %v", table, target, err)
				}
			}
			checks := []struct {
				query string
				want  int
			}{
				{`SELECT count(*) FROM config WHERE id=21 AND key='setting' AND value='preserved'`, 1},
				{`SELECT count(*) FROM services WHERE id=11 AND member_id=1 AND service='mon'`, 1},
				{`SELECT count(*) FROM client_config WHERE id=12 AND member_id=1 AND key='local-key' AND value='local-value'`, 1},
				{`SELECT count(*) FROM client_config WHERE id=13 AND member_id IS NULL AND key='global-key' AND value='global-value'`, 1},
			}
			if !emptyDisks {
				checks = append(checks, struct {
					query string
					want  int
				}{`SELECT count(*) FROM disks WHERE id=42 AND member_id=1 AND path='/dev/test'`, 1})
			}
			for _, check := range checks {
				var got int
				err = tx.QueryRow(check.query).Scan(&got)
				if err != nil || got != check.want {
					t.Fatalf("data was not preserved (%s): %d %v", check.query, got, err)
				}
			}
			result, err := tx.Exec(`INSERT INTO disks (member_id, path) VALUES (1, '/dev/new')`)
			if err != nil {
				t.Fatal(err)
			}
			id, err := result.LastInsertId()
			if err != nil || id != 101 {
				t.Fatalf("deleted disk IDs were reused: %d %v", id, err)
			}
			_, err = tx.Exec(`INSERT INTO client_config (member_id, key, value) VALUES (NULL, 'global-key', 'duplicate')`)
			if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
				t.Fatalf("global configuration uniqueness was lost: %v", err)
			}
			_, err = tx.Exec(`DELETE FROM core_cluster_members WHERE id=1`)
			if err != nil {
				t.Fatal(err)
			}
			var disks, services, configs int
			err = tx.QueryRow(`SELECT (SELECT count(*) FROM disks), (SELECT count(*) FROM services), (SELECT count(*) FROM client_config)`).Scan(&disks, &services, &configs)
			if err != nil || disks != 0 || services != 0 || configs != 1 {
				t.Fatalf("membership cascades or global config were lost: %d %d %d %v", disks, services, configs, err)
			}
			err = tx.Commit()
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
