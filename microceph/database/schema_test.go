package database

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/canonical/lxd/lxd/db/schema"
	_ "github.com/mattn/go-sqlite3"
)

func TestSchemaMicroclusterV2(t *testing.T) {
	for _, version := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprintf("from_quincy_schema_%d", version), func(t *testing.T) {
			db, err := sql.Open("sqlite3", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			exec := func(stmt string) {
				t.Helper()
				_, err := db.Exec(stmt)
				if err != nil {
					t.Fatal(err)
				}
			}

			if version == 0 {
				exec("CREATE TABLE core_cluster_members (id INTEGER PRIMARY KEY); INSERT INTO core_cluster_members VALUES (1)")
			} else {
				exec("CREATE TABLE internal_cluster_members (id INTEGER PRIMARY KEY); INSERT INTO internal_cluster_members VALUES (1)")
				// A frozen legacy fixture must not use the migrations being tested.
				legacy := []string{
					`CREATE TABLE config (id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, key TEXT NOT NULL UNIQUE, value TEXT NOT NULL);
CREATE TABLE disks (id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, member_id INTEGER NOT NULL, path TEXT NOT NULL, osd INTEGER NOT NULL UNIQUE, FOREIGN KEY (member_id) REFERENCES internal_cluster_members(id) ON DELETE CASCADE, UNIQUE(member_id, path));
CREATE TABLE services (id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, member_id INTEGER NOT NULL, service TEXT NOT NULL, FOREIGN KEY (member_id) REFERENCES internal_cluster_members(id) ON DELETE CASCADE, UNIQUE(member_id, service));
INSERT INTO config VALUES (1, 'cluster_network', '10.0.0.0/24');
INSERT INTO disks VALUES (7, 1, '/dev/sdb', 42);
INSERT INTO services VALUES (3, 1, 'mon');`,
					`CREATE TABLE client_config (id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, member_id INTEGER, key TEXT NOT NULL, value TEXT NOT NULL, FOREIGN KEY (member_id) REFERENCES internal_cluster_members(id) ON DELETE CASCADE);
CREATE UNIQUE INDEX cc_index ON client_config(coalesce(member_id, 0), key);
INSERT INTO client_config VALUES (5, 1, 'debug_ms', '1');
INSERT INTO client_config VALUES (6, NULL, 'debug_ms', '2');`,
					`CREATE TABLE disks2 (id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, member_id INTEGER NOT NULL, path TEXT NOT NULL, FOREIGN KEY (member_id) REFERENCES internal_cluster_members(id) ON DELETE CASCADE, UNIQUE(member_id, path));
INSERT INTO disks2 SELECT osd, member_id, path FROM disks;
DROP TABLE disks;
ALTER TABLE disks2 RENAME TO disks;`,
				}
				for _, stmt := range legacy[:version] {
					exec(stmt)
				}
				// Deleted allocations must not become reusable after table rebuilding.
				exec("INSERT INTO services VALUES (400, 1, 'mgr'); DELETE FROM services WHERE id=400")
				if version >= 2 {
					exec("INSERT INTO client_config VALUES (500, 1, 'temporary', '1'); DELETE FROM client_config WHERE id=500")
				}
				if version == 3 {
					exec("INSERT INTO disks VALUES (200, 1, '/dev/temporary'); DELETE FROM disks WHERE id=200")
				}
				exec("CREATE TABLE schema (id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, version INTEGER NOT NULL, updated_at DATETIME NOT NULL)")
				for v := 1; v <= version; v++ {
					exec(fmt.Sprintf("INSERT INTO schema (version, updated_at) VALUES (%d, strftime('%%s'))", v))
				}
				// Match microcluster v2's migration pragmas: renaming the core
				// table deliberately does not rewrite extension foreign keys.
				exec("PRAGMA foreign_keys=OFF; PRAGMA legacy_alter_table=ON; ALTER TABLE internal_cluster_members RENAME TO core_cluster_members")
			}

			_, err = schema.New(SchemaExtensions).Ensure(db)
			if err != nil {
				t.Fatal(err)
			}
			exec("PRAGMA foreign_keys=ON; PRAGMA legacy_alter_table=OFF")

			for _, table := range []string{"disks", "services", "client_config"} {
				var target string
				err := db.QueryRow("SELECT \"table\" FROM pragma_foreign_key_list(?)", table).Scan(&target)
				if err != nil {
					t.Fatal(err)
				}
				if target != "core_cluster_members" {
					t.Errorf("%s references %q, want core_cluster_members", table, target)
				}
			}
			if t.Failed() {
				return
			}

			minimumSequence := map[string]int{}
			if version > 0 {
				minimumSequence["services"] = 400
			}
			if version >= 2 {
				minimumSequence["client_config"] = 500
			}
			if version == 3 {
				minimumSequence["disks"] = 200
			}
			for table, minimum := range minimumSequence {
				var sequence int
				err := db.QueryRow("SELECT seq FROM sqlite_sequence WHERE name=?", table).Scan(&sequence)
				if err != nil || sequence < minimum {
					t.Errorf("%s allocation history lost: sequence=%d, want >=%d, err=%v", table, sequence, minimum, err)
				}
			}

			if version == 0 {
				exec("INSERT INTO config VALUES (1, 'cluster_network', '10.0.0.0/24'); INSERT INTO disks VALUES (42, 1, '/dev/sdb'); INSERT INTO services VALUES (3, 1, 'mon')")
			}
			if version < 2 {
				exec("INSERT INTO client_config VALUES (5, 1, 'debug_ms', '1'); INSERT INTO client_config VALUES (6, NULL, 'debug_ms', '2')")
			}
			for _, stmt := range []string{
				"INSERT INTO client_config(member_id, key, value) VALUES (1, 'debug_ms', 'duplicate')",
				"INSERT INTO client_config(member_id, key, value) VALUES (NULL, 'debug_ms', 'duplicate')",
				"INSERT INTO disks(member_id, path) VALUES (99, '/dev/invalid')",
			} {
				_, err := db.Exec(stmt)
				if err == nil {
					t.Errorf("constraint not enforced: %s", stmt)
				}
			}
			for _, stmt := range []string{
				"SELECT count(*) FROM disks WHERE id=42 AND member_id=1 AND path='/dev/sdb'",
				"SELECT count(*) FROM services WHERE id=3 AND member_id=1 AND service='mon'",
				"SELECT count(*) FROM config WHERE key='cluster_network' AND value='10.0.0.0/24'",
				"SELECT count(*) FROM client_config WHERE id=5 AND member_id=1 AND key='debug_ms' AND value='1'",
				"SELECT count(*) FROM client_config WHERE id=6 AND member_id IS NULL AND key='debug_ms' AND value='2'",
			} {
				var count int
				err := db.QueryRow(stmt).Scan(&count)
				if err != nil || count != 1 {
					t.Fatalf("data not preserved (%s): count=%d, err=%v", stmt, count, err)
				}
			}
			exec("DELETE FROM core_cluster_members WHERE id=1")
			for _, table := range []string{"disks", "services", "client_config"} {
				var count int
				err := db.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table+" WHERE member_id=1").Scan(&count)
				if err != nil || count != 0 {
					t.Fatalf("cascade failed for %s: count=%d, err=%v", table, count, err)
				}
			}
			var globalCount int
			err = db.QueryRow("SELECT count(*) FROM client_config WHERE member_id IS NULL").Scan(&globalCount)
			if err != nil || globalCount != 1 {
				t.Fatalf("global config lost: count=%d, err=%v", globalCount, err)
			}
		})
	}
}
