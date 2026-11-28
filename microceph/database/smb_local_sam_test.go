package database

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSMBLocalSAMStartedIsExactWriteAheadHistory(t *testing.T) {
	db := setupSMBDB(t)
	ctx := context.Background()
	require.NoError(t, reserveSMB(t, db, "alpha", "a"))
	read := func(member string) (bool, error) {
		var started bool
		err := smbTx(t, db, func(tx *sql.Tx) error {
			var err error
			started, err = GetSMBLocalSAMStarted(ctx, tx, "alpha", member)
			return err
		})
		return started, err
	}
	started, err := read("a")
	require.NoError(t, err)
	require.False(t, started)
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return StartSMBLocalSAM(ctx, tx, "alpha", "b") }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return StartSMBLocalSAM(ctx, tx, "alpha", "a") }))
	started, err = read("a")
	require.NoError(t, err)
	require.True(t, started)
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return ClearSMBLocalSAMStarted(ctx, tx, "alpha", "a") }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "a") }))
	started, err = read("a")
	require.NoError(t, err)
	require.True(t, started, "normal retirement cannot erase initialization uncertainty")
	// Another member's deliberate history must survive exact-member purge.
	require.NoError(t, reserveSMB(t, db, "alpha", "b"))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return StartSMBLocalSAM(ctx, tx, "alpha", "b") }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return RetireSMBOwner(ctx, tx, "alpha", "b") }))
	require.NoError(t, smbTx(t, db, func(tx *sql.Tx) error { return ClearSMBLocalSAMStarted(ctx, tx, "alpha", "a") }))
	started, err = read("a")
	require.NoError(t, err)
	require.False(t, started)
	started, err = read("b")
	require.NoError(t, err)
	require.True(t, started)
	_, err = db.Exec("DELETE FROM core_cluster_members WHERE name='b'; INSERT INTO core_cluster_members(name) VALUES ('b')")
	require.NoError(t, err)
	_, err = read("b")
	require.Error(t, err, "a reused name is not authority over historical identity")
	require.Error(t, reserveSMB(t, db, "alpha", "b"))
	require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return ClearSMBLocalSAMStarted(ctx, tx, "alpha", "b") }))
}

func TestSMBLocalSAMUnknownHistoryNeverMeansFresh(t *testing.T) {
	for _, raw := range []string{`{"owners":{"1":"a"}}`, `{"owners":{"1":"a"},"local_sam_started":null}`, `{"owners":{"1":"a"},"local_sam_started":{"bad":"a"}}`, `{"owners":{"1":"a"},"local_sam_started":{"1":""}}`} {
		t.Run(raw, func(t *testing.T) {
			db := setupSMBDB(t)
			_, err := db.Exec("INSERT INTO service_groups(service,group_id,config) VALUES('smb','alpha',?)", raw)
			require.NoError(t, err)
			require.Error(t, smbTx(t, db, func(tx *sql.Tx) error {
				_, err := GetSMBLocalSAMStarted(context.Background(), tx, "alpha", "a")
				return err
			}))
			require.Error(t, smbTx(t, db, func(tx *sql.Tx) error { return StartSMBLocalSAM(context.Background(), tx, "alpha", "a") }))
			var retained string
			require.NoError(t, db.QueryRow("SELECT config FROM service_groups").Scan(&retained))
			require.Equal(t, raw, retained)
		})
	}
}
