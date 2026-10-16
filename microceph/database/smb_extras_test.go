package database

import (
	"context"
	"database/sql"
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
	defer func() {
		// Best-effort cleanup; a successful commit already closes the transaction.
		_ = tx.Rollback()
	}()
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
				results <- err
				return
			}
			rank, err := ReserveSMBOwner(context.Background(), tx, "alpha", member, false)
			if err == nil && rank != 0 {
				err = fmt.Errorf("nonclustered reservation returned CTDB rank %d", rank)
			}
			if err == nil {
				err = tx.Commit()
			} else {
				// Preserve the reservation error while still releasing the transaction.
				_ = tx.Rollback()
			}
			results <- err
		}(member)
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err, "distinct members may concurrently reserve the same nonclustered group")
	}
	var raw string
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups WHERE group_id='alpha'").Scan(&raw))
	require.JSONEq(t, `{"clustered":false,"next_rank":0,"ranks":{},"owners":{"1":"a","2":"b"}}`, raw)
}

func TestSMBOfflineRemovalRetainsMemberUncertainty(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBOwner(ctx, tx, "alpha", "a", false)
		return err
	}))
	_, err := db.Exec("DELETE FROM core_cluster_members WHERE name='a'; INSERT INTO core_cluster_members(name) VALUES ('a')")
	require.NoError(t, err)
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBOwner(ctx, tx, "alpha", "b", false)
		return err
	}), "an uncertain owner must not block a different member of the same group")
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBOwner(ctx, tx, "alpha", "a", false)
		return err
	}), "a replacement identity cannot take over an uncertain old owner")
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBOwner(ctx, tx, "other", "a", false)
		return err
	}), "the uncertain member name must remain protected across groups")
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }))
	var raw string
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups WHERE group_id='alpha'").Scan(&raw))
	require.JSONEq(t, `{"clustered":false,"next_rank":0,"ranks":{},"owners":{"1":"a","2":"b"}}`, raw)
}

func TestSMBReservationModeAndUnownedRecord(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return RecordSMBService(ctx, tx, "missing", "a", `{}`) }))
	for _, clustered := range []bool{true, false} {
		t.Run(fmt.Sprintf("clustered=%t", clustered), func(t *testing.T) {
			db := setupSMBDB(t)
			require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
				_, err := ReserveSMBOwner(ctx, tx, "x", "a", clustered)
				return err
			}))
			require.Error(t, smbTx(t, db, func(tx *sql.Tx) error {
				_, err := ReserveSMBOwner(ctx, tx, "x", "b", !clustered)
				return err
			}))
			require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "x", "a") }))
			require.Error(t, smbTx(t, db, func(tx *sql.Tx) error {
				_, err := ReserveSMBOwner(ctx, tx, "x", "b", !clustered)
				return err
			}), "retirement must not permit a mode change")
		})
	}
}

func TestSMBNonclusteredReservationsDoNotAllocateCTDBRank(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	for _, member := range []string{"a", "b", "a"} {
		require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
			rank, err := ReserveSMBOwner(ctx, tx, "alpha", member, false)
			require.Zero(t, rank)
			return err
		}))
	}
	var raw string
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups WHERE group_id='alpha'").Scan(&raw))
	require.JSONEq(t, `{"clustered":false,"next_rank":0,"ranks":{},"owners":{"1":"a","2":"b"}}`, raw)
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBOwner(ctx, tx, "other", "a", false)
		return err
	}), "retry must preserve exclusive ownership of the host")
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
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, "alpha", "b", false); return err }))
	var raw string
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups WHERE group_id='alpha'").Scan(&raw))
	require.JSONEq(t, `{"clustered":false,"next_rank":0,"ranks":{},"owners":{"1":"a","2":"b"}}`, raw)
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBOwner(ctx, tx, "other", "a", false)
		return err
	}), "withdrawing a service row must retain exclusive ownership of the host")
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RecordSMBService(ctx, tx, "alpha", "a", `{}`) }))
}

func TestSMBCTDBViewUsesReservedOwnersAndPrivateAddresses(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	_, err := db.Exec(`UPDATE core_cluster_members SET address=CASE name WHEN 'a' THEN '192.0.2.1:7443' WHEN 'b' THEN '192.0.2.2:7443' ELSE '[2001:db8::3]:7443' END;`)
	require.NoError(t, err)
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		for _, member := range []string{"a", "b", "c"} {
			_, err := ReserveSMBOwner(ctx, tx, "alpha", member, true)
			require.NoError(t, err)
		}
		return RetireSMBOwner(ctx, tx, "alpha", "b")
	}))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		view, err := GetSMBCTDBView(ctx, tx, "alpha", "c")
		require.NoError(t, err)
		require.Equal(t, uint64(3), view.SlotCount)
		require.Equal(t, SMBCTDBMember{Identity: "3", PNN: 2, Node: "2001:db8::3", State: "new"}, view.Member)
		require.Equal(t, []SMBCTDBMember{{Identity: "1", PNN: 0, Node: "192.0.2.1", State: "new"}}, view.Peers)
		return nil
	}))
	_, err = db.Exec("DELETE FROM core_cluster_members WHERE name='a'")
	require.NoError(t, err)
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := GetSMBCTDBView(ctx, tx, "alpha", "c")
		return err
	}), "uncertain offline ownership must not silently disappear from a launch view")
}

func TestSMBRefreshObservationCannotCreatePlacement(t *testing.T) {
	ctx := context.Background()
	for _, clustered := range []bool{true, false} {
		t.Run(fmt.Sprintf("clustered=%t", clustered), func(t *testing.T) {
			db := setupSMBDB(t)
			require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, "alpha", "a", clustered); return err }))
			require.ErrorIs(t, smbTx(t, db, func(tx *sql.Tx) error { return UpdateSMBServiceInfo(ctx, tx, "alpha", "a", `{}`) }), sql.ErrNoRows)
			require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RecordSMBService(ctx, tx, "alpha", "a", `{}`) }))
			_, err := db.Exec(`CREATE TRIGGER forbid_refresh_reserve BEFORE UPDATE ON service_groups BEGIN SELECT RAISE(ABORT,'forbidden reservation'); END;
   CREATE TRIGGER forbid_refresh_insert BEFORE INSERT ON grouped_services BEGIN SELECT RAISE(ABORT,'forbidden placement'); END;`)
			require.NoError(t, err)
			require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
				mode, err := GetSMBConfiguredService(ctx, tx, "alpha", "a")
				require.NoError(t, err)
				require.Equal(t, clustered, mode)
				return UpdateSMBServiceInfo(ctx, tx, "alpha", "a", `{"smb_port":1445}`)
			}))
			var info string
			require.NoError(t, db.QueryRow("SELECT info FROM grouped_services").Scan(&info))
			require.JSONEq(t, `{"smb_port":1445}`, info)
			require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return UpdateSMBServiceInfo(ctx, tx, "alpha", "b", `{}`) }))
			_, err = db.Exec("DELETE FROM grouped_services")
			require.NoError(t, err)
			require.ErrorIs(t, smbTx(t, db, func(tx *sql.Tx) error { return UpdateSMBServiceInfo(ctx, tx, "alpha", "a", `{}`) }), sql.ErrNoRows)
		})
	}
}

func TestSMBMembershipAddressReuseRequiresRetirement(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	_, err := db.Exec(`UPDATE core_cluster_members SET address=CASE name WHEN 'a' THEN '192.0.2.1:7443' WHEN 'b' THEN '192.0.2.2:7443' ELSE '192.0.2.3:7443' END;`)
	require.NoError(t, err)
	reserve := func(member string) error {
		return smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, "alpha", member, true); return err })
	}
	require.NoError(t, reserve("a"))
	_, err = db.Exec(`UPDATE core_cluster_members SET address='192.0.2.1:7443' WHERE name='b'`)
	require.NoError(t, err)
	require.Error(t, reserve("b"), "another identity cannot reuse a retained owner's endpoint")
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }))
	require.NoError(t, reserve("b"), "a verified retired endpoint may be reused with a new PNN")
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		view, err := GetSMBCTDBView(ctx, tx, "alpha", "b")
		require.NoError(t, err)
		require.Equal(t, uint64(1), view.Member.PNN)
		require.Equal(t, uint64(2), view.SlotCount)
		return nil
	}))
	// Simulate offline forced removal, which cannot retire an owner.
	_, err = db.Exec(`DELETE FROM core_cluster_members WHERE name='b'; UPDATE core_cluster_members SET address='192.0.2.1:7443' WHERE name='c'`)
	require.NoError(t, err)
	require.Error(t, reserve("c"), "retain endpoint evidence after the core member row disappears")
}

func TestSMBOwnerRemovalFindsFailedReservations(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBOwner(ctx, tx, "alpha", "a", false)
		require.NoError(t, err)
		groups, err := GetSMBOwnerGroups(ctx, tx, "a")
		require.Equal(t, []string{"alpha"}, groups, "no successful service row is required")
		return err
	}))
	_, err := db.Exec(`DELETE FROM core_cluster_members WHERE name='a'; INSERT INTO core_cluster_members(name) VALUES('a')`)
	require.NoError(t, err)
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := GetSMBOwnerGroups(ctx, tx, "a"); return err }))
}
