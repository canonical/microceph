package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
)

func TestSMBLocalSAMJointLossAndExactRetiredPurge(t *testing.T) {
	for _, failure := range []string{"none", "filesystem", "sql-clear", "active-unowned", "foreign-owner"} {
		t.Run(failure, func(t *testing.T) {
			state, runner := smbLocalStartupFixture(t, "")
			require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
			require.NoError(t, DisableSMB(context.Background(), state, "alpha", false))
			root := smbRoot("alpha")
			// Even loss of every local artifact must not erase SQL SAM history.
			require.NoError(t, os.RemoveAll(root))
			runner.commands = nil
			require.ErrorContains(t, enableSMBLocked(context.Background(), state, "alpha"), "local SAM")
			for _, command := range runner.commands {
				require.NotContains(t, command, " accounts ")
				require.NotContains(t, command, "snapctl start")
			}
			// Retire again to exercise historical-only purge, without owner.json.
			require.NoError(t, DisableSMB(context.Background(), state, "alpha", false))
			oldRemove := removeSMBLocalStateFunc
			t.Cleanup(func() { removeSMBLocalStateFunc = oldRemove })
			mockDB := state.ClusterState().Database().(*mocks.MockDB)
			oldTx := mockDB.TxFn
			if failure == "filesystem" {
				removeSMBLocalStateFunc = func(string) error { return errors.New("purge filesystem failure") }
			}
			if failure == "sql-clear" {
				mockDB.TxFn = func(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
					return oldTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
						err := fn(ctx, tx)
						if err != nil {
							return err
						}
						started, err := database.GetSMBLocalSAMStarted(ctx, tx, "alpha", "a")
						if err == nil && !started {
							return errors.New("purge clear commit failure")
						}
						return err
					})
				}
			}
			if failure == "active-unowned" {
				runner.active = true
			}
			if failure == "foreign-owner" {
				require.NoError(t, smbAtomicWrite(filepath.Join(smbBase(), "owner.json"), []byte("other")))
			}
			runner.commands = nil
			err := DisableSMB(context.Background(), state, "alpha", true)
			if failure != "none" {
				require.Error(t, err)
				var started bool
				require.NoError(t, oldTx(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
					var err error
					started, err = database.GetSMBLocalSAMStarted(ctx, tx, "alpha", "a")
					return err
				}))
				require.True(t, started, "failed destruction or SQL clear must retain uncertainty")
				removeSMBLocalStateFunc = oldRemove
				mockDB.TxFn = oldTx
				runner.active = false
				if failure == "foreign-owner" {
					require.NoError(t, os.Remove(filepath.Join(smbBase(), "owner.json")))
				}
				require.NoError(t, DisableSMB(context.Background(), state, "alpha", true), "exact historical purge retry remains reachable")
			} else {
				require.NoError(t, err)
			}
			for _, command := range runner.commands {
				require.NotContains(t, command, "snapctl stop", "historical-only purge cannot stop an unowned app")
			}
			require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"), "explicit successful purge permits genuinely fresh initialization")
		})
	}
}

func TestSMBLocalSAMWriteAheadCancellationCannotBootstrapOnRetry(t *testing.T) {
	state, runner := smbLocalStartupFixture(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mockDB := state.ClusterState().Database().(*mocks.MockDB)
	oldTx := mockDB.TxFn
	mockDB.TxFn = func(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
		err := oldTx(ctx, fn)
		if err != nil {
			return err
		}
		var raw string
		require.NoError(t, runner.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
		var config database.SMBServiceGroupConfig
		require.NoError(t, json.Unmarshal([]byte(raw), &config))
		if config.LocalSAMStarted["1"] == "a" {
			cancel()
		}
		return nil
	}
	require.Error(t, enableSMBLocked(ctx, state, "alpha"))
	for _, command := range runner.commands {
		require.NotContains(t, command, " accounts ")
		require.NotContains(t, command, "snapctl start")
	}
	mockDB.TxFn = oldTx
	require.ErrorContains(t, enableSMBLocked(context.Background(), state, "alpha"), "local SAM")
	require.NoError(t, DisableSMB(context.Background(), state, "alpha", true))
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
}
