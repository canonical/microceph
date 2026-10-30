package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func setupSMBDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "smb.db")+"?_foreign_keys=on&_txlock=immediate&_busy_timeout=10000")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`CREATE TABLE core_cluster_members (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT UNIQUE, address TEXT);
 INSERT INTO core_cluster_members(name,address) VALUES ('a','192.0.2.1:7443'), ('b','192.0.2.2:7443'), ('c','192.0.2.3:7443');`)
	require.NoError(t, err)
	tx, err := db.Begin()
	require.NoError(t, err)
	require.NoError(t, schemaUpdate6(context.Background(), tx))
	require.NoError(t, tx.Commit())
	return db
}

func smbTx(t *testing.T, db *sql.DB, fn func(*sql.Tx) error) error {
	t.Helper()
	tx, err := db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	err = fn(tx)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func reserveSMB(t *testing.T, db *sql.DB, group, member string) error {
	t.Helper()
	return smbTx(t, db, func(tx *sql.Tx) error {
		return ReserveSMBOwner(context.Background(), tx, group, member)
	})
}

func TestSMBReservationBeforeReadinessAndRetention(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, reserveSMB(t, db, "alpha", "a"))
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
	require.Zero(t, count, "a reservation must not advertise a successful service")
	require.Error(t, reserveSMB(t, db, "other", "a"))
	require.NoError(t, reserveSMB(t, db, "alpha", "a"), "exact retry")
	for _, info := range []string{`{"address":"192.0.2.1"}`, `{"address":"192.0.2.2"}`} {
		require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RecordSMBService(ctx, tx, "alpha", "a", info) }))
	}
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
	require.Equal(t, 1, count)
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return RecordSMBService(ctx, tx, "alpha", "b", `{}`) }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }))
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM service_groups").Scan(&count))
	require.Equal(t, 1, count, "disable must not delete the logical group")
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }), "cleanup retry after SQL retirement")
	require.NoError(t, reserveSMB(t, db, "alpha", "b"))
}

func TestSMBNonclusteredSQLRace(t *testing.T) {
	db := setupSMBDB(t)
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, member := range []string{"a", "b"} {
		wg.Add(1)
		go func(member string) {
			defer wg.Done()
			<-start
			tx, err := db.Begin()
			if err != nil {
				results <- fmt.Errorf("begin reservation for %s: %w", member, err)
				return
			}
			err = ReserveSMBOwner(context.Background(), tx, "alpha", member)
			if err == nil {
				err = tx.Commit()
				if err != nil {
					err = fmt.Errorf("commit reservation for %s: %w", member, err)
				}
			} else {
				_ = tx.Rollback()
				err = fmt.Errorf("reserve owner %s: %w", member, err)
			}
			results <- err
		}(member)
	}
	close(start)
	wg.Wait()
	close(results)
	successes, rejected := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.Contains(t, err.Error(), "Nonclustered SMB cluster")
			rejected++
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, rejected)
	var raw string
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups WHERE group_id='alpha'").Scan(&raw))
	var config SMBServiceGroupConfig
	require.NoError(t, json.Unmarshal([]byte(raw), &config))
	require.Len(t, config.Owners, 1)
}

func TestSMBOfflineRemovalRetainsMemberUncertainty(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, reserveSMB(t, db, "alpha", "a"))
	_, err := db.Exec("DELETE FROM core_cluster_members WHERE name='a'; INSERT INTO core_cluster_members(name) VALUES ('a')")
	require.NoError(t, err)
	require.ErrorContains(t, reserveSMB(t, db, "alpha", "b"), `already has owner "a"`)
	require.Error(t, reserveSMB(t, db, "alpha", "a"), "replacement identity cannot take over uncertain owner")
	require.Error(t, reserveSMB(t, db, "other", "a"), "uncertain name remains protected across groups")
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }))
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := GetSMBOwnerGroups(ctx, tx, "a"); return err }))
	var raw string
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
	require.JSONEq(t, `{"owners":{"1":"a"},"local_sam_started":{}}`, raw)
}

func TestSMBStoppedServiceWithdrawsRowNotOwner(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, reserveSMB(t, db, "alpha", "a"))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RecordSMBService(ctx, tx, "alpha", "a", `{}`) }))
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return WithdrawSMBService(ctx, tx, "alpha", "b") }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return WithdrawSMBService(ctx, tx, "alpha", "a") }))
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
	require.Zero(t, count)
	require.Error(t, reserveSMB(t, db, "alpha", "b"))
	require.Error(t, reserveSMB(t, db, "other", "a"))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RecordSMBService(ctx, tx, "alpha", "a", `{}`) }))
}

func TestSMBOwnerRemovalFindsFailedReservations(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, reserveSMB(t, db, "alpha", "a"))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		groups, err := GetSMBOwnerGroups(ctx, tx, "a")
		require.Equal(t, []string{"alpha"}, groups, "no successful service row required")
		return err
	}))
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return RecordSMBService(ctx, tx, "missing", "a", `{}`) }))
}

func TestSMBNonclusteredRelocationAndIndependentIDs(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, reserveSMB(t, db, "alpha", "a"))
	require.NoError(t, reserveSMB(t, db, "beta", "b"))
	require.Error(t, reserveSMB(t, db, "alpha", "c"), "failed startup blocks replacement")
	require.NoError(t, reserveSMB(t, db, "alpha", "a"))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }))
	require.NoError(t, reserveSMB(t, db, "alpha", "c"), "verified stop before retirement")
	require.Error(t, reserveSMB(t, db, "beta", "c"), "one logical cluster per host")
}

func TestSMBLegacyNonclusteredOwnersCannotGrow(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO service_groups(service,group_id,config) VALUES('smb','alpha','{"owners":{"1":"a","2":"b"}}')`)
	require.NoError(t, err)
	for _, member := range []string{"a", "b"} {
		require.NoError(t, reserveSMB(t, db, "alpha", member))
	}
	require.ErrorContains(t, reserveSMB(t, db, "alpha", "c"), "Nonclustered SMB cluster")
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		warning, err := GetSMBPolicyWarning(ctx, tx, "alpha")
		require.Contains(t, warning, "policy-noncompliant")
		require.Contains(t, warning, "alpha")
		return err
	}))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "b") }))
	require.Error(t, reserveSMB(t, db, "alpha", "c"))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		warning, err := GetSMBPolicyWarning(ctx, tx, "alpha")
		require.Empty(t, warning)
		return err
	}))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }))
	require.NoError(t, reserveSMB(t, db, "alpha", "c"))
}

func TestSMBOwnershipRejectsUnsupportedRetainedStateBeforeMutation(t *testing.T) {
	for _, raw := range []string{`{}`, `{"owners":null}`, `{"owners":{}} {"owners":{"1":"a"}}`, `{"owners":{}} trailing`, `{"owners":{"1":"a"},"clustered":true}`, `{"owners":{"1":"a"},"ad":{}}`, `{"owners":{"1":"a"},"auth_identity":"domain"}`} {
		t.Run(raw, func(t *testing.T) {
			db := setupSMBDB(t)
			ctx := context.Background()
			_, err := db.Exec("INSERT INTO service_groups(service,group_id,config) VALUES('smb','alpha',?)", raw)
			require.NoError(t, err)
			require.Error(t, reserveSMB(t, db, "alpha", "a"))
			require.Error(t, reserveSMB(t, db, "beta", "b"), "unknown state cannot be ignored across groups")
			require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }))
			var retained string
			require.NoError(t, db.QueryRow("SELECT config FROM service_groups").Scan(&retained))
			require.Equal(t, raw, retained)
		})
	}
}
