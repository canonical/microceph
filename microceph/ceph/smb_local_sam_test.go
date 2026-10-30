package ceph

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSMBRetainedLocalSAMLossRefusedBeforeAccounts(t *testing.T) {
	for _, lost := range []string{"passdb.tdb", "secrets.tdb", "both", "fence", "empty", "symlink"} {
		t.Run(lost, func(t *testing.T) {
			state, runner := smbLocalStartupFixture(t, "")
			// This is deliberately shareless and has no users: it still owns a SAM.
			require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
			require.NoError(t, DisableSMB(context.Background(), state, "alpha", false))
			root := smbRoot("alpha")
			private := filepath.Join(root, "samba/private")
			switch lost {
			case "both":
				require.NoError(t, os.Remove(filepath.Join(private, "passdb.tdb")))
				require.NoError(t, os.Remove(filepath.Join(private, "secrets.tdb")))
			case "fence":
				// Before the fix no fence exists, which is exactly the regression.
				_ = os.Remove(filepath.Join(root, "identity/local-sam"))
			case "empty":
				require.NoError(t, os.WriteFile(filepath.Join(private, "secrets.tdb"), nil, 0600))
			case "symlink":
				require.NoError(t, os.Remove(filepath.Join(private, "secrets.tdb")))
				require.NoError(t, os.Symlink(filepath.Join(private, "passdb.tdb"), filepath.Join(private, "secrets.tdb")))
			default:
				require.NoError(t, os.Remove(filepath.Join(private, lost)))
			}
			runner.commands = nil
			err := enableSMBLocked(context.Background(), state, "alpha")
			require.ErrorContains(t, err, "local SAM")
			require.False(t, runner.active)
			for _, command := range runner.commands {
				require.NotContains(t, command, " accounts ")
				require.NotContains(t, command, "snapctl start")
			}
			var count int
			require.NoError(t, runner.db.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
			require.Zero(t, count)
		})
	}
}

func TestSMBFailedFirstLocalSAMImportCannotAuthorizeReinitialization(t *testing.T) {
	state, runner := smbLocalStartupFixture(t, "accounts")
	require.Error(t, enableSMBLocked(context.Background(), state, "alpha"))
	runner.failure = ""
	runner.commands = nil
	err := enableSMBLocked(context.Background(), state, "alpha")
	require.ErrorContains(t, err, "local SAM")
	for _, command := range runner.commands {
		require.NotContains(t, command, " accounts ")
		require.NotContains(t, command, "snapctl start")
	}
}
