package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

const testSMBADIdentity = `{"realm":"EXAMPLE.TEST","workgroup":"EXAMPLE","netbios_name":"ALPHA","fqdn":"alpha.example.test"}`

func TestSMBADHistoryIsAdditiveAndStrict(t *testing.T) {
	base := map[string]any{
		"owners": map[string]string{"1": "a"}, "local_sam_started": map[string]string{},
		"clustered": true, "next_rank": 1, "ranks": map[string]uint64{"1": 0},
		"endpoints": map[string]string{"1": "192.0.2.1"}, "auth_identity": testSMBADIdentity,
		"ad": map[string]any{"join_owner": "1", "joined": true, "initialized": true},
	}
	raw, err := json.Marshal(base)
	require.NoError(t, err)
	config, err := decodeSMBOwnership(string(raw))
	require.NoError(t, err, "valid AD history must coexist with current local and rank fields")
	encoded, err := json.Marshal(config)
	require.NoError(t, err)
	require.JSONEq(t, string(raw), string(encoded))
	for _, field := range []string{"join_owner", "joined", "initialized"} {
		t.Run("missing-"+field, func(t *testing.T) {
			state := map[string]any{"join_owner": "1", "joined": true, "initialized": true}
			delete(state, field)
			base["ad"] = state
			corrupt, err := json.Marshal(base)
			require.NoError(t, err)
			_, err = decodeSMBOwnership(string(corrupt))
			require.Error(t, err, "partial AD history must never become fresh authorization")
		})
	}
}

func TestSMBADJoinExclusionAndRetention(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	identity := testSMBADIdentity
	for _, member := range []string{"a", "b"} {
		require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
			_, err := ReserveSMBADOwner(ctx, tx, "alpha", member, identity)
			return err
		}))
	}
	start := make(chan struct{})
	type joinResult struct {
		member string
		err    error
	}
	results := make(chan joinResult, 2)
	var wg sync.WaitGroup
	for _, member := range []string{"a", "b"} {
		wg.Add(1)
		go func(member string) {
			defer wg.Done()
			<-start
			err := smbTx(t, db, func(tx *sql.Tx) error { return BeginSMBADJoin(ctx, tx, "alpha", member) })
			results <- joinResult{member: member, err: err}
		}(member)
	}
	close(start)
	wg.Wait()
	close(results)
	outcomes := make(map[string]error, 2)
	winners := []string{}
	for result := range results {
		outcomes[result.member] = result.err
		if result.err == nil {
			winners = append(winners, result.member)
		}
	}
	require.Len(t, winners, 1, "concurrent join transaction outcomes: %v", outcomes)
	winner := winners[0]
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
	require.ErrorContains(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBClusteredOwner(ctx, tx, "alpha", winner); return err }), "identity")
	require.ErrorContains(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBADOwner(ctx, tx, "alpha", winner, "another-realm")
		return err
	}), "identity")
	require.ErrorContains(t, smbTx(t, db, func(tx *sql.Tx) error { return CheckSMBADPurge(ctx, tx, "alpha", loser) }), "final")
}

func TestSMBADCannotConvertLegacyUserIdentity(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { _, err := ReserveSMBClusteredOwner(ctx, tx, "alpha", "a"); return err }))
	require.ErrorContains(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBADOwner(ctx, tx, "alpha", "b", testSMBADIdentity)
		return err
	}), "identity")
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }))
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBADOwner(ctx, tx, "alpha", "a", testSMBADIdentity)
		return err
	}))
}

func TestSMBADMissingAttemptRecordIsNotReconstructed(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBADOwner(ctx, tx, "alpha", "a", testSMBADIdentity)
		return err
	}))
	var raw string
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
	var config map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &config))
	delete(config, "ad")
	data, err := json.Marshal(config)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE service_groups SET config=?", string(data))
	require.NoError(t, err)
	require.ErrorContains(t, smbTx(t, db, func(tx *sql.Tx) error {
		_, err := ReserveSMBADOwner(ctx, tx, "alpha", "a", testSMBADIdentity)
		return err
	}), "history")
}

func TestSMBADPurgeRequiresExactRetainedOwner(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	for _, member := range []string{"a", "b"} {
		require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
			_, err := ReserveSMBADOwner(ctx, tx, "alpha", member, testSMBADIdentity)
			return err
		}))
	}
	for _, mutate := range []func(context.Context, *sql.Tx, string, string) error{BeginSMBADJoin, CompleteSMBADJoin, InitializeSMBAD} {
		require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return mutate(ctx, tx, "alpha", "a") }))
	}
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return CheckSMBADPurge(ctx, tx, "alpha", "c") }), "unowned member is not purge authority")
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return CheckSMBADPurge(ctx, tx, "alpha", "a") }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }))
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return CheckSMBADPurge(ctx, tx, "alpha", "a") }), "retired retry must not relax final-owner guard")
}

func TestSMBADCorruptHistoryNeverAuthorizesMutation(t *testing.T) {
	for _, kind := range []string{"missing-ad", "null-ad", "missing-identity", "invalid-realm", "initialized-without-join", "joined-without-actor", "invalid-actor", "lost-actor-rank"} {
		t.Run(kind, func(t *testing.T) {
			db := setupSMBDB(t)
			ctx := context.Background()
			require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error {
				_, err := ReserveSMBADOwner(ctx, tx, "alpha", "a", testSMBADIdentity)
				return err
			}))
			require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return BeginSMBADJoin(ctx, tx, "alpha", "a") }))
			var raw string
			require.NoError(t, db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
			var config map[string]any
			require.NoError(t, json.Unmarshal([]byte(raw), &config))
			ad := config["ad"].(map[string]any)
			switch kind {
			case "missing-ad":
				delete(config, "ad")
			case "null-ad":
				config["ad"] = nil
			case "missing-identity":
				delete(config, "auth_identity")
			case "invalid-realm":
				config["auth_identity"] = `{"realm":".","workgroup":"EXAMPLE","netbios_name":"ALPHA","fqdn":"alpha.."}`
			case "initialized-without-join":
				ad["initialized"] = true
			case "joined-without-actor":
				ad["joined"] = true
				ad["join_owner"] = ""
			case "invalid-actor":
				ad["join_owner"] = "01"
			case "lost-actor-rank":
				ad["join_owner"] = "2"
			}
			corrupted, err := json.Marshal(config)
			require.NoError(t, err)
			_, err = decodeSMBOwnership(string(corrupted))
			require.Error(t, err, "corrupt retained state is not a valid history")
			_, err = db.Exec("UPDATE service_groups SET config=?", string(corrupted))
			require.NoError(t, err)
			require.Error(t, smbTx(t, db, func(tx *sql.Tx) error {
				_, err := ReserveSMBADOwner(ctx, tx, "alpha", "a", testSMBADIdentity)
				return err
			}))
			require.NoError(t, db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
			require.Equal(t, string(corrupted), raw, "refusal must retain corrupt evidence byte-for-byte")
		})
	}
}
