package database

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSMBManagedOperationAdmissionExpiryAndOwnership(t *testing.T) {
	db := setupGroupedServiceDB(t)
	ctx := context.Background()
	claim := func(group, token string, now, expires int64) bool {
		tx := smbRankTx(t, db)
		acquired, err := AcquireSMBManagedOperation(ctx, tx, group, token, now, expires)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
		return acquired
	}
	release := func(token string) bool {
		tx := smbRankTx(t, db)
		released, err := ReleaseSMBManagedOperation(ctx, tx, "files", token)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
		return released
	}
	require.True(t, claim("files", "old", 100, 200))
	tx := smbRankTx(t, db)
	mode, err := GetSMBPlacementMode(ctx, tx, "files")
	require.NoError(t, err)
	require.Nil(t, mode, "admission is not a placement or clustering-mode claim")
	var receipts int
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM grouped_services`).Scan(&receipts))
	require.Zero(t, receipts)
	require.NoError(t, tx.Rollback())
	require.False(t, claim("files", "contender", 199, 300))
	require.True(t, claim("other", "independent", 199, 300))
	require.True(t, claim("files", "replacement", 200, 300))
	require.False(t, release("old"), "an expired owner must not release its replacement")
	require.False(t, claim("files", "third", 201, 301))
	require.True(t, release("replacement"))
	require.False(t, release("replacement"))
	var groups int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM service_groups WHERE group_id='files'`).Scan(&groups))
	require.Zero(t, groups, "a claim without deployment metadata is removed on release")
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM service_groups WHERE group_id='other'`).Scan(&groups))
	require.Equal(t, 1, groups)
}

func TestSMBManagedOperationRejectsConcurrentClaimants(t *testing.T) {
	db := setupGroupedServiceDB(t)
	// SQLite serializes these transactions; they represent requests from different
	// members sharing the database, not a process-local admission mutex.
	db.SetMaxOpenConns(1)
	type outcome struct {
		acquired bool
		err      error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	for _, token := range []string{"node-a", "node-b"} {
		go func() {
			<-start
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				results <- outcome{err: err}
				return
			}
			defer func() { _ = tx.Rollback() }()
			acquired, err := AcquireSMBManagedOperation(context.Background(), tx, "files", token, 100, 200)
			if err == nil {
				err = tx.Commit()
			}
			results <- outcome{acquired: acquired, err: err}
		}()
	}
	close(start)
	winners := 0
	for range 2 {
		result := <-results
		require.NoError(t, result.err)
		if result.acquired {
			winners++
		}
	}
	require.Equal(t, 1, winners)
}

func TestSMBManagedOperationReleasePreservesDeploymentAndFinalization(t *testing.T) {
	db := setupGroupedServiceDB(t)
	ctx := context.Background()
	tx := smbRankTx(t, db)
	acquired, err := AcquireSMBManagedOperation(ctx, tx, "files", "owner", 100, 200)
	require.NoError(t, err)
	require.True(t, acquired)
	reserveSMBTestRank(t, tx, "node-a", `{"features":["clustered"]}`)
	released, err := ReleaseSMBManagedOperation(ctx, tx, "files", "owner")
	require.NoError(t, err)
	require.True(t, released)
	var stored string
	require.NoError(t, tx.QueryRow(`SELECT config FROM service_groups WHERE group_id='files'`).Scan(&stored))
	require.JSONEq(t, `{"desired_spec":{"features":["clustered"]},"ctdb_ranks":{"smb.files.node-a":0},"next_ctdb_rank":1}`, stored)
	acquired, err = AcquireSMBManagedOperation(ctx, tx, "files", "delete", 100, 200)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, finalizeSMBServiceGroup(ctx, tx, "files"))
	acquired, err = AcquireSMBManagedOperation(ctx, tx, "files", "competing", 100, 200)
	require.NoError(t, err)
	require.False(t, acquired, "callback finalization must not release the outer request's lease")
	released, err = ReleaseSMBManagedOperation(ctx, tx, "files", "delete")
	require.NoError(t, err)
	require.True(t, released)
	var count int
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM service_groups`).Scan(&count))
	require.Zero(t, count)
}

func TestSMBManagedOperationReleaseNeverCascadesMemberReceipts(t *testing.T) {
	db := setupGroupedServiceDB(t)
	_, err := db.Exec(`
INSERT INTO service_groups(service,group_id,config) VALUES ('smb','files','');
INSERT INTO grouped_services(service_group_id,member_id,info)
VALUES ((SELECT id FROM service_groups WHERE group_id='files'), 1, '{}');`)
	require.NoError(t, err)
	tx := smbRankTx(t, db)
	acquired, err := AcquireSMBManagedOperation(context.Background(), tx, "files", "owner", 100, 200)
	require.NoError(t, err)
	require.True(t, acquired)
	released, err := ReleaseSMBManagedOperation(context.Background(), tx, "files", "owner")
	require.NoError(t, err)
	require.True(t, released)
	var count int
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM service_groups`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM grouped_services`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestSMBManagedOperationRejectsInvalidState(t *testing.T) {
	db := setupGroupedServiceDB(t)
	ctx := context.Background()
	tx := smbRankTx(t, db)
	_, err := AcquireSMBManagedOperation(ctx, tx, "files", "", 100, 200)
	require.Error(t, err)
	_, err = AcquireSMBManagedOperation(ctx, tx, "files", "owner", 100, 100)
	require.Error(t, err)
	_, err = tx.Exec(`INSERT INTO service_groups(service,group_id,config) VALUES ('smb','files','bad-json')`)
	require.NoError(t, err)
	_, err = AcquireSMBManagedOperation(ctx, tx, "files", "owner", 100, 200)
	require.Error(t, err)
	_, err = ReleaseSMBManagedOperation(ctx, tx, "files", "owner")
	require.Error(t, err)
	_, err = tx.Exec(`UPDATE service_groups SET config='{"managed_operation":{"token":"","expires_at":200}}'`)
	require.NoError(t, err)
	_, err = AcquireSMBManagedOperation(ctx, tx, "files", "owner", 100, 200)
	require.ErrorContains(t, err, "invalid stored SMB managed-operation lease")
}

func TestSMBPlacementWritesPreserveManagedOperation(t *testing.T) {
	db := setupGroupedServiceDB(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO service_groups (service, group_id, config) VALUES
('smb', 'files', '{"desired_spec":null,"managed_operation":{"token":"owner","expires_at":1200}}')`)
	require.NoError(t, err)
	tx := smbRankTx(t, db)
	reserveSMBTestRank(t, tx, "node-a", `{"features":["clustered"]}`)
	var stored string
	require.NoError(t, tx.QueryRow(`SELECT config FROM service_groups WHERE group_id='files'`).Scan(&stored))
	var group map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(stored), &group))
	require.JSONEq(t, `{"token":"owner","expires_at":1200}`, string(group["managed_operation"]))
	// A placement's old snapshot must neither erase nor replace admission ownership.
	err = addOrUpdateGroupedService(ctx, tx, "node-a", "smb", "files",
		`{"desired_spec":{"features":["clustered"]},"managed_operation":{"token":"stale","expires_at":9999}}`,
		`{"ctdb_rank":0,"ctdb_identity":"smb.files.node-a"}`)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow(`SELECT config FROM service_groups WHERE group_id='files'`).Scan(&stored))
	require.NoError(t, json.Unmarshal([]byte(stored), &group))
	require.JSONEq(t, `{"token":"owner","expires_at":1200}`, string(group["managed_operation"]))
}

func TestSMBFinalizationRetainsManagedOperationUntilRequestFinishes(t *testing.T) {
	db := setupGroupedServiceDB(t)
	_, err := db.Exec(`INSERT INTO service_groups (service, group_id, config) VALUES
('smb', 'files', '{"desired_spec":{"features":["clustered"]},"ctdb_ranks":{"smb.files.node-a":0},"next_ctdb_rank":1,"managed_operation":{"token":"owner","expires_at":1200}}')`)
	require.NoError(t, err)
	tx := smbRankTx(t, db)
	require.NoError(t, finalizeSMBServiceGroup(context.Background(), tx, "files"))
	var stored string
	require.NoError(t, tx.QueryRow(`SELECT config FROM service_groups WHERE group_id='files'`).Scan(&stored))
	require.JSONEq(t, `{"desired_spec":null,"managed_operation":{"token":"owner","expires_at":1200}}`, stored)
}
