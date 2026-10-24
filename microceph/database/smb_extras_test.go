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
	require.Empty(t, config.Ranks)
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
	require.ErrorContains(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBOwner(ctx, tx, "alpha", "b", false)
		return err
	}), `already has owner "a"`)
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
	require.JSONEq(t, `{"clustered":false,"next_rank":0,"ranks":{},"owners":{"1":"a"}}`, raw)
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
	for _, member := range []string{"a", "a"} {
		require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
			rank, err := ReserveSMBOwner(ctx, tx, "alpha", member, false)
			require.Zero(t, rank)
			return err
		}))
	}
	var raw string
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups WHERE group_id='alpha'").Scan(&raw))
	require.JSONEq(t, `{"clustered":false,"next_rank":0,"ranks":{},"owners":{"1":"a"}}`, raw)
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
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, "alpha", "b", false); return err }))
	var raw string
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups WHERE group_id='alpha'").Scan(&raw))
	require.JSONEq(t, `{"clustered":false,"next_rank":0,"ranks":{},"owners":{"1":"a"}}`, raw)
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

func TestSMBNonclusteredRelocationAndIndependentIDs(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	reserve := func(cluster, member string) error {
		return smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, cluster, member, false); return err })
	}
	require.NoError(t, reserve("alpha", "a"))
	require.NoError(t, reserve("beta", "b"))
	require.Error(t, reserve("alpha", "c"), "failed startup reservation blocks replacement without success row")
	require.NoError(t, reserve("alpha", "a"), "exact retry")
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }))
	require.NoError(t, reserve("alpha", "c"), "caller verified stop before explicit retirement")
	require.Error(t, reserve("beta", "c"), "one logical cluster per host")
}

func TestSMBLegacyNonclusteredOwnersCannotGrow(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO service_groups(service,group_id,config) VALUES('smb','alpha','{"clustered":false,"next_rank":4,"ranks":{"99":3},"owners":{"1":"a","2":"b"}}')`)
	require.NoError(t, err)
	for _, member := range []string{"a", "b"} {
		require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, "alpha", member, false); return err }))
	}
	require.ErrorContains(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, "alpha", "c", false); return err }), "Nonclustered SMB cluster")
	var raw string
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
	require.JSONEq(t, `{"clustered":false,"next_rank":4,"ranks":{"99":3},"owners":{"1":"a","2":"b"}}`, raw)
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "b") }))
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, "alpha", "c", false); return err }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, "alpha", "c", false); return err }))
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
	require.JSONEq(t, `{"clustered":false,"next_rank":4,"ranks":{"99":3},"owners":{"3":"c"}}`, raw, "historical ranks do not block relocation or get discarded")
}

func TestSMBLegacyPolicyDiagnosticUsesReservations(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO service_groups(service,group_id,config) VALUES('smb','alpha','{"clustered":false,"next_rank":0,"ranks":{},"owners":{"1":"a","99":"offline"}}')`)
	require.NoError(t, err)
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		warning, err := GetSMBPolicyWarning(ctx, tx, "alpha")
		require.Contains(t, warning, "policy-noncompliant")
		require.Contains(t, warning, "offline")
		require.Contains(t, warning, "alpha")
		return err
	}))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		warning, err := GetSMBPolicyWarning(ctx, tx, "alpha")
		require.Empty(t, warning)
		return err
	}))
}

func TestSMBADJoinExclusionAndRetention(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	identity := `{"realm":"EXAMPLE.TEST","workgroup":"EXAMPLE","netbios_name":"ALPHA"}`
	for _, member := range []string{"a", "b"} {
		require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
			_, err := ReserveSMBADOwner(ctx, tx, "alpha", member, identity)
			return err
		}))
	}
	start := make(chan struct{})
	results := make(chan string, 2)
	var wg sync.WaitGroup
	for _, member := range []string{"a", "b"} {
		wg.Add(1)
		go func(member string) {
			defer wg.Done()
			<-start
			err := smbTx(t, db, func(tx *sql.Tx) error { return BeginSMBADJoin(ctx, tx, "alpha", member) })
			if err == nil {
				results <- member
			} else {
				results <- ""
			}
		}(member)
	}
	close(start)
	wg.Wait()
	close(results)
	winner := ""
	for member := range results {
		if member != "" {
			require.Empty(t, winner)
			winner = member
		}
	}
	require.NotEmpty(t, winner)
	// A failed/timed out invocation, same-owner retry, retirement and elapsed time
	// never release an attempt. No lease or automatic takeover exists.
	for _, member := range []string{"a", "b"} {
		require.ErrorContains(t, smbTx(t, db, func(tx *sql.Tx) error { return BeginSMBADJoin(ctx, tx, "alpha", member) }), "uncertain")
	}
	loser := "a"
	if winner == "a" {
		loser = "b"
	}
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return CompleteSMBADJoin(ctx, tx, "alpha", loser) }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return CompleteSMBADJoin(ctx, tx, "alpha", winner) }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		state, err := GetSMBADState(ctx, tx, "alpha", loser)
		require.True(t, state.Joined)
		require.False(t, state.Initialized)
		return err
	}))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return InitializeSMBAD(ctx, tx, "alpha", winner) }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", winner) }))
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return BeginSMBADJoin(ctx, tx, "alpha", loser) }))
	require.ErrorContains(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, "alpha", winner, true); return err }), "identity")
	require.ErrorContains(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBADOwner(ctx, tx, "alpha", winner, "another-realm")
		return err
	}), "identity")
	require.ErrorContains(t, smbTx(t, db, func(tx *sql.Tx) error { return CheckSMBADPurge(ctx, tx, "alpha", loser) }), "final")
}

func TestSMBADCannotConvertLegacyUserIdentity(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBOwner(ctx, tx, "alpha", "a", true); return err }))
	require.ErrorContains(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBADOwner(ctx, tx, "alpha", "b", "EXAMPLE.TEST"); return err }), "identity")
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }))
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBADOwner(ctx, tx, "alpha", "a", "EXAMPLE.TEST"); return err }))
}

func TestSMBADMissingAttemptRecordIsNotReconstructed(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBADOwner(ctx, tx, "alpha", "a", "EXAMPLE.TEST"); return err }))
	var raw string
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
	var config map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &config))
	delete(config, "ad")
	data, err := json.Marshal(config)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE service_groups SET config=?", string(data))
	require.NoError(t, err)
	require.ErrorContains(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBADOwner(ctx, tx, "alpha", "a", "EXAMPLE.TEST"); return err }), "state lost")
}
