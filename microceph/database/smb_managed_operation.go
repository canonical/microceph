package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// AcquireSMBManagedOperation claims an idle or expired SMB group in the caller's
// transaction. Times are Unix seconds; expiry permits reclaim, not cancellation
// of previously dispatched Ceph work. No deployment receipt is created.
func AcquireSMBManagedOperation(ctx context.Context, tx *sql.Tx, groupID, token string, now, expiresAt int64) (bool, error) {
	if token == "" || expiresAt <= now {
		return false, fmt.Errorf("invalid SMB managed-operation lease")
	}
	group, err := readSMBManagedOperationGroup(ctx, tx, groupID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if group.ManagedOperation != nil && group.ManagedOperation.ExpiresAt > now {
		return false, nil
	}
	group.ManagedOperation = &SMBManagedOperation{Token: token, ExpiresAt: expiresAt}
	encoded, err := json.Marshal(group)
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO service_groups(service, group_id, config) VALUES ('smb', ?, ?)
ON CONFLICT(service, group_id) DO UPDATE SET config = excluded.config
`, groupID, string(encoded))
	return err == nil, err
}

// ReleaseSMBManagedOperation clears only the matching owner's claim. A metadata-
// only row is removed when safe; successful or pending deployment state is kept.
func ReleaseSMBManagedOperation(ctx context.Context, tx *sql.Tx, groupID, token string) (bool, error) {
	group, err := readSMBManagedOperationGroup(ctx, tx, groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if group.ManagedOperation == nil || group.ManagedOperation.Token != token {
		return false, nil
	}
	group.ManagedOperation = nil
	if (len(group.DesiredSpec) == 0 || string(group.DesiredSpec) == "null") && len(group.CTDBRanks) == 0 && group.NextCTDBRank == 0 {
		result, err := tx.ExecContext(ctx, `
DELETE FROM service_groups WHERE service = 'smb' AND group_id = ?
AND NOT EXISTS (SELECT 1 FROM grouped_services WHERE service_group_id = service_groups.id)
`, groupID)
		if err != nil {
			return false, err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if deleted != 0 {
			return true, nil
		}
	}
	encoded, err := json.Marshal(group)
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE service_groups SET config = ? WHERE service = 'smb' AND group_id = ?`, string(encoded), groupID)
	return err == nil, err
}

func readSMBManagedOperationGroup(ctx context.Context, tx *sql.Tx, groupID string) (SMBServiceGroupConfig, error) {
	var group SMBServiceGroupConfig
	var encoded string
	err := tx.QueryRowContext(ctx, `SELECT coalesce(nullif(config, ''), '{}') FROM service_groups WHERE service = 'smb' AND group_id = ?`, groupID).Scan(&encoded)
	if err != nil {
		return group, err
	}
	err = json.Unmarshal([]byte(encoded), &group)
	if err != nil {
		return group, fmt.Errorf("invalid stored SMB group configuration")
	}
	if group.ManagedOperation != nil && (group.ManagedOperation.Token == "" || group.ManagedOperation.ExpiresAt <= 0) {
		return group, fmt.Errorf("invalid stored SMB managed-operation lease")
	}
	return group, nil
}
