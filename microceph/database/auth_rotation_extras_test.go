package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupAuthRotationDB creates an in-memory SQLite database with the auth_rotation
// table by running the real schemaUpdate10 migration.
func setupAuthRotationDB(t *testing.T) *sql.DB {
	t.Helper()
	db := setupPlacementDB(t)

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = schemaUpdate10(context.Background(), tx)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	return db
}

func TestAuthRotationDefault(t *testing.T) {
	db := setupAuthRotationDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	rec, err := GetAuthRotation(context.Background(), tx)
	require.NoError(t, err)
	assert.Equal(t, AuthRotationStateIdle, rec.State)
	assert.Equal(t, "", rec.Stage)
	assert.Equal(t, "", rec.TargetKeyType)
	assert.Equal(t, "", rec.ClientName)
	assert.Equal(t, "", rec.StepProgress)
	assert.Equal(t, "", rec.Blocker)
	assert.Equal(t, "", rec.Detail)
	assert.Equal(t, int64(0), rec.ApplyLockToken)

	// Singleton constraint check: inserting another row should fail.
	_, err = tx.ExecContext(context.Background(), `INSERT INTO auth_rotation (id) VALUES (2)`)
	require.Error(t, err)
}

func TestSetAndGetAuthRotation(t *testing.T) {
	db := setupAuthRotationDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	rec := AuthRotationRecord{
		TargetKeyType: "aes256k",
		State:         AuthRotationStateInProgress,
		Stage:         AuthRotationStageRotateDaemons,
		ClientName:    "client.rgw",
		StepProgress:  `{"completed_daemons":["mon.a"]}`,
		Blocker:       "",
		Detail:        "rotating daemons",
	}

	err = SetAuthRotation(context.Background(), tx, rec)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	tx2, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx2.Rollback() }()

	loaded, err := GetAuthRotation(context.Background(), tx2)
	require.NoError(t, err)
	assert.Equal(t, "aes256k", loaded.TargetKeyType)
	assert.Equal(t, AuthRotationStateInProgress, loaded.State)
	assert.Equal(t, AuthRotationStageRotateDaemons, loaded.Stage)
	assert.Equal(t, "client.rgw", loaded.ClientName)
	assert.Equal(t, `{"completed_daemons":["mon.a"]}`, loaded.StepProgress)
	assert.Equal(t, "", loaded.Blocker)
	assert.Equal(t, "rotating daemons", loaded.Detail)
}

func TestResetAuthRotation(t *testing.T) {
	db := setupAuthRotationDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	rec := AuthRotationRecord{
		TargetKeyType: "aes256k",
		State:         AuthRotationStateCompleted,
		Stage:         AuthRotationStageFinishSafely,
		ClientName:    "",
		StepProgress:  "{}",
		Blocker:       "",
		Detail:        "complete",
	}
	err = SetAuthRotation(context.Background(), tx, rec)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	tx2, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = ResetAuthRotation(context.Background(), tx2)
	require.NoError(t, err)
	require.NoError(t, tx2.Commit())

	tx3, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx3.Rollback() }()

	loaded, err := GetAuthRotation(context.Background(), tx3)
	require.NoError(t, err)
	assert.Equal(t, AuthRotationStateIdle, loaded.State)
	assert.Equal(t, "", loaded.Stage)
	assert.Equal(t, "", loaded.TargetKeyType)
	assert.Equal(t, "", loaded.ClientName)
	assert.Equal(t, "", loaded.StepProgress)
}

func TestInitOrResumeAuthRotation(t *testing.T) {
	db := setupAuthRotationDB(t)

	// 1. Fresh start from idle.
	tx1, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	rec, resumed, err := InitOrResumeAuthRotation(context.Background(), tx1, "aes256k", "")
	require.NoError(t, err)
	assert.False(t, resumed)
	assert.Equal(t, "aes256k", rec.TargetKeyType)
	assert.Equal(t, AuthRotationStateInProgress, rec.State)
	assert.Equal(t, AuthRotationStageReadiness, rec.Stage)
	require.NoError(t, tx1.Commit())

	// 2. Advance stage.
	tx2, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = SetAuthRotationStage(context.Background(), tx2, AuthRotationStageRotateDaemons, `{"done":["mon.a"]}`)
	require.NoError(t, err)
	require.NoError(t, tx2.Commit())

	// 3. Re-running with the SAME key type resumes unfinished work.
	tx3, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	rec, resumed, err = InitOrResumeAuthRotation(context.Background(), tx3, "aes256k", "")
	require.NoError(t, err)
	assert.True(t, resumed)
	assert.Equal(t, AuthRotationStageRotateDaemons, rec.Stage)
	assert.Equal(t, `{"done":["mon.a"]}`, rec.StepProgress)
	require.NoError(t, tx3.Commit())

	// 4. Re-running with a DIFFERENT target key type while incomplete fails.
	tx4, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx4.Rollback() }()
	_, _, err = InitOrResumeAuthRotation(context.Background(), tx4, "aes", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `incomplete rotation to key type "aes256k" is in progress`)

	// 5. Re-running with a DIFFERENT client filter while incomplete fails.
	_, _, err = InitOrResumeAuthRotation(context.Background(), tx4, "aes256k", "client.custom")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `cannot change client`)
}

func TestInitOrResumeFromBlockedAndFailed(t *testing.T) {
	db := setupAuthRotationDB(t)

	// Set state to blocked
	tx1, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	_, _, err = InitOrResumeAuthRotation(context.Background(), tx1, "aes256k", "")
	require.NoError(t, err)
	err = SetAuthRotationBlocker(context.Background(), tx1, "Unmanaged credentials must be rotated manually", "client.external")
	require.NoError(t, err)
	require.NoError(t, tx1.Commit())

	// Resuming unblocks and resets state to in_progress
	tx2, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	rec, resumed, err := InitOrResumeAuthRotation(context.Background(), tx2, "aes256k", "")
	require.NoError(t, err)
	assert.True(t, resumed)
	assert.Equal(t, AuthRotationStateInProgress, rec.State)
	assert.Equal(t, "", rec.Blocker)
	assert.Equal(t, "", rec.Detail)
	require.NoError(t, tx2.Commit())
}

func TestInitOrResumeAfterCompleted(t *testing.T) {
	db := setupAuthRotationDB(t)

	tx1, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	rec, _, err := InitOrResumeAuthRotation(context.Background(), tx1, "aes256k", "")
	require.NoError(t, err)
	rec.State = AuthRotationStateCompleted
	rec.Stage = AuthRotationStageFinishSafely
	err = SetAuthRotation(context.Background(), tx1, *rec)
	require.NoError(t, err)
	require.NoError(t, tx1.Commit())

	// After completion, invoking rotation starts a fresh rotation even with a different key type
	tx2, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	freshRec, resumed, err := InitOrResumeAuthRotation(context.Background(), tx2, "aes", "")
	require.NoError(t, err)
	assert.False(t, resumed)
	assert.Equal(t, "aes", freshRec.TargetKeyType)
	assert.Equal(t, AuthRotationStateInProgress, freshRec.State)
	assert.Equal(t, AuthRotationStageReadiness, freshRec.Stage)
	require.NoError(t, tx2.Commit())
}

func TestAuthRotationLockAcquireReleaseAndContention(t *testing.T) {
	db := setupAuthRotationDB(t)

	now := time.Now().UnixNano()
	leaseDuration := int64(60 * time.Second)
	token1 := now
	staleBefore := now - leaseDuration

	// 1. Acquire free lock.
	lockTx(t, db, func(tx *sql.Tx) {
		acquired, err := TryAcquireAuthRotationLock(context.Background(), tx, token1, staleBefore)
		require.NoError(t, err)
		assert.True(t, acquired)
	})

	// 2. Contention: second acquirer while token1 is live fails.
	token2 := now + int64(100*time.Millisecond)
	lockTx(t, db, func(tx *sql.Tx) {
		acquired, err := TryAcquireAuthRotationLock(context.Background(), tx, token2, staleBefore)
		require.NoError(t, err)
		assert.False(t, acquired)
	})

	// 3. Stale reclaim: holder is older than staleBefore cutoff.
	token3 := now + int64(2*time.Minute)
	futureCutoff := token1 + int64(1*time.Second) // token1 is now considered stale
	lockTx(t, db, func(tx *sql.Tx) {
		acquired, err := TryAcquireAuthRotationLock(context.Background(), tx, token3, futureCutoff)
		require.NoError(t, err)
		assert.True(t, acquired)
	})

	// 4. Token1 attempts release after being reclaimed: should fail (return false).
	lockTx(t, db, func(tx *sql.Tx) {
		released, err := ReleaseAuthRotationLock(context.Background(), tx, token1)
		require.NoError(t, err)
		assert.False(t, released)
	})

	// 5. Token3 releases held lock: should succeed (return true).
	lockTx(t, db, func(tx *sql.Tx) {
		released, err := ReleaseAuthRotationLock(context.Background(), tx, token3)
		require.NoError(t, err)
		assert.True(t, released)
	})

	// 6. Token3 releases again: returns false because lock is already free.
	lockTx(t, db, func(tx *sql.Tx) {
		released, err := ReleaseAuthRotationLock(context.Background(), tx, token3)
		require.NoError(t, err)
		assert.False(t, released)
	})
}
