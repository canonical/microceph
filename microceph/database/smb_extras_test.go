package database

import (
	"context"
	"database/sql"
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
	_, err = db.Exec(`CREATE TABLE core_cluster_members (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT UNIQUE);
 INSERT INTO core_cluster_members(name) VALUES ('a'), ('b'), ('c');`)
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
	defer tx.Rollback()
	err = fn(tx)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func TestSMBReservationBeforeReadinessAndRetention(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		rank, err := ReserveSMBOwner(ctx, tx, "alpha", "a", true)
		require.Equal(t, uint64(0), rank)
		return err
	}))
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
	require.Zero(t, count, "a reservation must not advertise a successful service")
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBOwner(ctx, tx, "other", "a", true)
		return err
	}))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		rank, err := ReserveSMBOwner(ctx, tx, "alpha", "a", true)
		require.Equal(t, uint64(0), rank, "retry must retain the rank")
		return err
	}))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		return RecordSMBService(ctx, tx, "alpha", "a", `{"address":"192.0.2.1"}`)
	}))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		return RecordSMBService(ctx, tx, "alpha", "a", `{"address":"192.0.2.2"}`)
	}))
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }))
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM service_groups").Scan(&count))
	require.Equal(t, 1, count, "disable must not delete group/rank history")
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		rank, err := ReserveSMBOwner(ctx, tx, "alpha", "b", true)
		require.Equal(t, uint64(1), rank, "retired PNNs are never assigned to another identity")
		return err
	}))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		rank, err := ReserveSMBOwner(ctx, tx, "alpha", "a", true)
		require.Equal(t, uint64(0), rank)
		return err
	}))
}

func TestSMBSingletonSQLRace(t *testing.T) {
	db := setupSMBDB(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, member := range []string{"a", "b"} {
		wg.Add(1)
		go func(member string) {
			defer wg.Done()
			tx, err := db.Begin()
			if err != nil {
				results <- err
				return
			}
			_, err = ReserveSMBOwner(context.Background(), tx, "single", member, false)
			if err == nil {
				err = tx.Commit()
			} else {
				tx.Rollback()
			}
			results <- err
		}(member)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	require.Equal(t, 1, success)
}

func TestSMBOfflineRemovalRetainsUncertainty(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBOwner(ctx, tx, "single", "a", false)
		return err
	}))
	_, err := db.Exec("DELETE FROM core_cluster_members WHERE name='a'; INSERT INTO core_cluster_members(name) VALUES ('a')")
	require.NoError(t, err)
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBOwner(ctx, tx, "single", "b", false)
		return err
	}))
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBOwner(ctx, tx, "single", "a", false)
		return err
	}), "a replacement identity cannot take over an uncertain old owner")
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "single", "a") }))
}

func TestSMBReservationModeAndUnownedRecord(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return RecordSMBService(ctx, tx, "missing", "a", `{}`) }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, "x", "a", true); return err }))
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, "x", "b", false); return err }))
}

func TestSMBSingletonDoesNotAllocateCTDBRank(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, "single", "a", false); return err }))
	var raw string
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups WHERE group_id='single'").Scan(&raw))
	require.JSONEq(t, `{"clustered":false,"next_rank":0,"ranks":{},"owners":{"1":"a"}}`, raw)
}

func TestSMBStoppedUpdateWithdrawsRowNotOwner(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, "alpha", "a", false); return err }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RecordSMBService(ctx, tx, "alpha", "a", `{}`) }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return WithdrawSMBService(ctx, tx, "alpha", "a") }))
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
	require.Zero(t, count)
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, "alpha", "b", false); return err }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RecordSMBService(ctx, tx, "alpha", "a", `{}`) }))
}
