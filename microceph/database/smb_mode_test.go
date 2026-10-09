package database

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func withGroupTx(t *testing.T, db *sql.DB, fn func(context.Context, *sql.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	fn(ctx, tx)
	require.NoError(t, tx.Commit())
}

func TestGetSMBPlacementModeUsesStoredSpec(t *testing.T) {
	db := setupGroupedServiceDB(t)
	withGroupTx(t, db, func(ctx context.Context, tx *sql.Tx) {
		mode, err := GetSMBPlacementMode(ctx, tx, "absent")
		require.NoError(t, err)
		require.Nil(t, mode)
		for _, tc := range []struct {
			id, config       string
			known, clustered bool
		}{
			{"unset", `{"desired_spec":null}`, false, false},
			{"direct", `{"desired_spec":{"spec":{"features":[]}}}`, true, false},
			{"nested", `{"desired_spec":{"spec":{"features":["clustered"]}}}`, true, true},
			{"flat", `{"desired_spec":{"features":["clustered"]}}`, true, true},
		} {
			_, err := tx.ExecContext(ctx, `INSERT INTO service_groups(service, group_id, config) VALUES ('smb', ?, ?)`, tc.id, tc.config)
			require.NoError(t, err)
			mode, err = GetSMBPlacementMode(ctx, tx, tc.id)
			require.NoError(t, err)
			if tc.known {
				require.NotNil(t, mode)
				require.Equal(t, tc.clustered, *mode)
			} else {
				require.Nil(t, mode)
			}
		}
	})
}

func TestSMBPlacementChecksSharedModeAndSingleMember(t *testing.T) {
	for _, clustered := range []bool{true, false} {
		db := setupGroupedServiceDB(t)
		config := `{"desired_spec":{"spec":{"features":[]}}}`
		if clustered {
			config = `{"desired_spec":{"spec":{"features":["clustered"]}}}`
		}
		withGroupTx(t, db, func(ctx context.Context, tx *sql.Tx) {
			require.NoError(t, addOrUpdateGroupedService(ctx, tx, "node-a", "smb", "files", config, `{}`))
			require.NoError(t, CheckSMBPlacementMode(ctx, tx, "files", "node-a", clustered))
			require.ErrorContains(t, CheckSMBPlacementMode(ctx, tx, "files", "node-a", !clustered), "cannot be changed")
			err := CheckSMBPlacementMode(ctx, tx, "files", "node-b", clustered)
			if clustered {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "one member")
			}
		})
	}
}
