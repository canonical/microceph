package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSMBClusteredOwnershipRetainsRankAndEndpointEvidence(t *testing.T) {
	raw := `{"owners":{"1":"a","2":"b"},"local_sam_started":{},"clustered":true,"next_rank":3,"ranks":{"1":0,"2":2},"endpoints":{"1":"192.0.2.1","2":"192.0.2.2"}}`
	config, err := decodeSMBOwnership(raw)
	require.NoError(t, err)
	encoded, err := json.Marshal(config)
	require.NoError(t, err)
	require.JSONEq(t, raw, string(encoded), "retired rank holes and endpoint evidence must survive observation")
}

func TestSMBLocalReservationCannotConsumeClusteredHistory(t *testing.T) {
	db := setupSMBDB(t)
	raw := `{"owners":{"1":"a"},"local_sam_started":{},"clustered":true,"next_rank":1,"ranks":{"1":0},"endpoints":{"1":"192.0.2.1"}}`
	_, err := db.Exec("INSERT INTO service_groups(service,group_id,config) VALUES('smb','alpha',?)", raw)
	require.NoError(t, err)
	require.Error(t, reserveSMB(t, db, "alpha", "a"))
	var retained string
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups WHERE group_id='alpha'").Scan(&retained))
	require.Equal(t, raw, retained, "mode mismatch must not rewrite rank/identity evidence")
}

func reserveSMBClustered(t *testing.T, db *sql.DB, group, member string) (uint64, error) {
	t.Helper()
	var rank uint64
	err := smbTx(t, db, func(tx *sql.Tx) error {
		var err error
		rank, err = ReserveSMBClusteredOwner(context.Background(), tx, group, member)
		return err
	})
	return rank, err
}

func TestSMBClusteredReservationsKeepRankHistory(t *testing.T) {
	db := setupSMBDB(t)
	for i, member := range []string{"a", "b", "c"} {
		rank, err := reserveSMBClustered(t, db, "alpha", member)
		require.NoError(t, err)
		require.Equal(t, uint64(i), rank)
	}
	ctx := context.Background()
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		return RetireSMBOwner(ctx, tx, "alpha", "a")
	}))
	rank, err := reserveSMBClustered(t, db, "alpha", "a")
	require.NoError(t, err)
	require.Zero(t, rank, "exact-identity rejoin retains its original PNN")
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		return RetireSMBOwner(ctx, tx, "alpha", "a")
	}))
	_, err = db.Exec("DELETE FROM core_cluster_members WHERE name='a'; INSERT INTO core_cluster_members(id,name,address) VALUES(4,'a','192.0.2.1:7443')")
	require.NoError(t, err)
	rank, err = reserveSMBClustered(t, db, "alpha", "a")
	require.NoError(t, err)
	require.Equal(t, uint64(3), rank, "a replacement database identity cannot recycle its old PNN")
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
	require.Zero(t, count, "reservations must never advertise successful services")
}

func TestSMBClusteredViewUsesActualOwnersAndRetainsHoles(t *testing.T) {
	db := setupSMBDB(t)
	for _, member := range []string{"a", "b", "c"} {
		_, err := reserveSMBClustered(t, db, "alpha", member)
		require.NoError(t, err)
	}
	ctx := context.Background()
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		return RetireSMBOwner(ctx, tx, "alpha", "b")
	}))
	var view *SMBCTDBView
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		var err error
		view, err = GetSMBCTDBView(ctx, tx, "alpha", "a")
		return err
	}))
	raw, err := json.Marshal(view)
	require.NoError(t, err)
	require.JSONEq(t, `{"slot_count":3,"member":{"identity":"1","pnn":0,"node":"192.0.2.1","state":"new"},"peers":[{"identity":"3","pnn":2,"node":"192.0.2.3","state":"new"}]}`, string(raw))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		groups, err := GetSMBRecoveryGroups(ctx, tx, "a")
		require.NoError(t, err)
		require.Equal(t, []string{"alpha"}, groups, "failed/pending owners still need their recovery client")
		groups, err = GetSMBRecoveryGroups(ctx, tx, "b")
		require.NoError(t, err)
		require.Empty(t, groups, "retired owners must not render recovery credentials")
		return nil
	}))
}

func TestSMBClusteredCorruptHistoryRefusesReservationBeforeMutation(t *testing.T) {
	for _, scenario := range []string{"counter-rewind", "counter-below-active", "duplicate-retired-rank", "invalid-history-identity", "local-sam-history", "invalid-endpoint"} {
		t.Run(scenario, func(t *testing.T) {
			db := setupSMBDB(t)
			for _, member := range []string{"a", "b"} {
				_, err := reserveSMBClustered(t, db, "alpha", member)
				require.NoError(t, err)
			}
			require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
				return RetireSMBOwner(context.Background(), tx, "alpha", "a")
			}))
			var raw string
			require.NoError(t, db.QueryRow("SELECT config FROM service_groups WHERE group_id='alpha'").Scan(&raw))
			var config map[string]any
			require.NoError(t, json.Unmarshal([]byte(raw), &config))
			switch scenario {
			case "counter-rewind":
				config["next_rank"] = 0
			case "counter-below-active":
				config["next_rank"] = 1
			case "duplicate-retired-rank":
				config["ranks"].(map[string]any)["1"] = 1
			case "invalid-history-identity":
				config["ranks"].(map[string]any)["not-a-member-id"] = 0
			case "local-sam-history":
				config["local_sam_started"] = map[string]string{"1": "a"}
			case "invalid-endpoint":
				config["endpoints"].(map[string]any)["1"] = "0.0.0.0"
			}
			data, err := json.Marshal(config)
			require.NoError(t, err)
			_, err = db.Exec("UPDATE service_groups SET config=? WHERE group_id='alpha'", string(data))
			require.NoError(t, err)
			_, err = reserveSMBClustered(t, db, "alpha", "b")
			require.Error(t, err)
			require.NoError(t, db.QueryRow("SELECT config FROM service_groups WHERE group_id='alpha'").Scan(&raw))
			require.Equal(t, string(data), raw, "uncertainty must not become reconstructed rank/identity state")
		})
	}
}

func TestSMBClusteredEmptyPersistedHistoryIsNotBootstrapPermission(t *testing.T) {
	db := setupSMBDB(t)
	raw := `{"owners":{},"local_sam_started":{},"clustered":true,"ranks":{},"endpoints":{}}`
	_, err := db.Exec("INSERT INTO service_groups(service,group_id,config) VALUES('smb','alpha',?)", raw)
	require.NoError(t, err)
	_, err = reserveSMBClustered(t, db, "alpha", "a")
	require.Error(t, err)
	var retained string
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups WHERE group_id='alpha'").Scan(&retained))
	require.Equal(t, raw, retained)
}

func TestSMBClusteredOwnersAreNotALocalPolicyViolation(t *testing.T) {
	db := setupSMBDB(t)
	for _, member := range []string{"a", "b"} {
		_, err := reserveSMBClustered(t, db, "alpha", member)
		require.NoError(t, err)
	}
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		warning, err := GetSMBPolicyWarning(context.Background(), tx, "alpha")
		require.NoError(t, err)
		require.Empty(t, warning)
		return nil
	}))
}

func TestSMBClusteredReservationDoesNotAuthorizeLocalSAMInitialization(t *testing.T) {
	db := setupSMBDB(t)
	_, err := reserveSMBClustered(t, db, "alpha", "a")
	require.NoError(t, err)
	ctx := context.Background()
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := GetSMBLocalSAMStarted(ctx, tx, "alpha", "a")
		return err
	}))
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error {
		return StartSMBLocalSAM(ctx, tx, "alpha", "a")
	}))
	var raw string
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups WHERE group_id='alpha'").Scan(&raw))
	require.Contains(t, raw, `"local_sam_started":{}`)
}
