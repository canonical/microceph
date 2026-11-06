package database

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/canonical/lxd/lxd/db/schema"
	"github.com/canonical/microcluster/cluster"
	_ "github.com/mattn/go-sqlite3"
)

func TestSchemaExtensionsAreOrdered(t *testing.T) {
	want := []schema.Update{schemaUpdate1, schemaUpdate2, schemaUpdate3, schemaUpdate4}
	if len(SchemaExtensions) != len(want) {
		t.Fatal("unexpected application schema length")
	}
	for i, update := range want {
		got := SchemaExtensions[i]
		if got == nil || reflect.ValueOf(got).Pointer() != reflect.ValueOf(update).Pointer() {
			t.Fatalf("application update %d is out of order", i+1)
		}
	}
}

// These SQLite tests exercise application DDL and statements, not dqlite upgrades.
func TestApplicationSchemaOnCoreMembership(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "populated disk migration"}[populated], func(t *testing.T) {
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
			for _, update := range []schema.Update{schemaUpdate1, schemaUpdate2} {
				err = update(ctx, tx)
				if err != nil {
					t.Fatal(err)
				}
			}
			if populated {
				_, err = tx.Exec(`INSERT INTO disks (id, member_id, path, osd) VALUES (7, 1, '/dev/test', 42); INSERT INTO services (member_id, service) VALUES (1, 'mon'); INSERT INTO client_config (member_id, key, value) VALUES (1, 'test', 'value');`)
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, update := range []schema.Update{schemaUpdate3, schemaUpdate4} {
				err = update(ctx, tx)
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, code := range []int{diskObjects, serviceObjects, clientConfigItemObjects} {
				stmt, stmtErr := cluster.StmtString(code)
				if stmtErr != nil {
					t.Fatal(stmtErr)
				}
				prepared, prepareErr := tx.PrepareContext(ctx, stmt)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				prepared.Close()
			}
			if populated {
				var id int
				var path string
				err = tx.QueryRow(`SELECT id, path FROM disks WHERE member_id=1`).Scan(&id, &path)
				if err != nil || id != 42 || path != "/dev/test" {
					t.Fatalf("disk migration lost OSD identity or path: %d %q %v", id, path, err)
				}
			}
			err = tx.Commit()
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
