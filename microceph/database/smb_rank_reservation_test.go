package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func smbRankTx(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := tx.Rollback()
		if err != sql.ErrTxDone {
			require.NoError(t, err)
		}
	})
	return tx
}

func reserveSMBTestRank(t *testing.T, tx *sql.Tx, node, spec string) (SMBServiceGroupConfig, int) {
	t.Helper()
	config, rank, err := ReserveSMBCTDBRank(context.Background(), tx, "files", []byte(spec), "smb.files."+node)
	require.NoError(t, err)
	return config, rank
}

func TestReserveSMBCTDBRankCommitsReservationBeforeMemberReceipt(t *testing.T) {
	db := setupGroupedServiceDB(t)
	tx := smbRankTx(t, db)
	config, rank := reserveSMBTestRank(t, tx, "node-a", `{"service_id":"files"}`)
	require.Equal(t, 0, rank)
	require.Equal(t, map[string]int{"smb.files.node-a": 0}, config.CTDBRanks)
	require.Equal(t, 1, config.NextCTDBRank)
	var receipts int
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM grouped_services`).Scan(&receipts))
	require.Zero(t, receipts)
	require.NoError(t, tx.Commit())

	tx = smbRankTx(t, db)
	config, rank = reserveSMBTestRank(t, tx, "node-b", `{"service_id":"files","revision":2}`)
	require.Equal(t, 1, rank)
	require.Equal(t, map[string]int{"smb.files.node-a": 0, "smb.files.node-b": 1}, config.CTDBRanks)
	require.Equal(t, 2, config.NextCTDBRank)
	config, rank = reserveSMBTestRank(t, tx, "node-a", `{"service_id":"files","revision":3}`)
	require.Equal(t, 0, rank)
	require.Equal(t, 2, config.NextCTDBRank)
}

func TestFinalizeSMBServiceGroupPreservesState(t *testing.T) {
	for _, tc := range []struct {
		reason   string
		receipts int
	}{
		{"SMB service records remain", 1},
		{"state changed during teardown", 0},
		{"finalization rejected", 0},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			db := setupGroupedServiceDB(t)
			tx := smbRankTx(t, db)
			config, _ := reserveSMBTestRank(t, tx, "node-a", `{"features":["clustered"]}`)
			encoded, err := json.Marshal(config)
			require.NoError(t, err)
			snapshot := string(encoded)
			switch tc.reason {
			case "SMB service records remain":
				err := addOrUpdateGroupedService(context.Background(), tx, "node-a", "smb", "files", snapshot, `{"ctdb_rank":0,"ctdb_identity":"smb.files.node-a"}`)
				require.NoError(t, err)
			case "state changed during teardown":
				reserveSMBTestRank(t, tx, "node-b", `{"features":["clustered"]}`)
			case "finalization rejected":
				_, err := tx.Exec(`CREATE TRIGGER reject_smb_finalization BEFORE DELETE ON service_groups
WHEN OLD.service = 'smb' AND OLD.group_id = 'files'
BEGIN SELECT RAISE(ABORT, 'finalization rejected'); END;`)
				require.NoError(t, err)
			}
			require.NoError(t, tx.Commit())

			tx = smbRankTx(t, db)
			require.ErrorContains(t, finalizeSMBServiceGroup(context.Background(), tx, "files", &snapshot), tc.reason)
			require.NoError(t, tx.Rollback())
			var groups, receipts int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM service_groups WHERE service = 'smb' AND group_id = 'files'`).Scan(&groups))
			require.Equal(t, 1, groups)
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM grouped_services`).Scan(&receipts))
			require.Equal(t, tc.receipts, receipts)
		})
	}
}

func TestFinalizeSMBServiceGroupClearsZeroReceiptReservationsForSameIDRecreation(t *testing.T) {
	db := setupGroupedServiceDB(t)
	tx := smbRankTx(t, db)
	reserveSMBTestRank(t, tx, "node-a", `{"features":["clustered"]}`)
	require.NoError(t, tx.Commit())
	tx = smbRankTx(t, db)
	require.NoError(t, finalizeSMBServiceGroup(context.Background(), tx, "files"))
	require.NoError(t, tx.Commit())
	tx = smbRankTx(t, db)
	config, rank := reserveSMBTestRank(t, tx, "node-a", `{"features":[]}`)
	require.Equal(t, 0, rank)
	require.JSONEq(t, `{"features":[]}`, string(config.DesiredSpec))
	// The recreated group's reservation must still obey transaction rollback.
	require.NoError(t, tx.Rollback())
	var groups int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM service_groups`).Scan(&groups))
	require.Zero(t, groups)
}
