package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/microceph/microceph/logger"
)

// Auth rotation states.
const (
	// AuthRotationStateIdle means no rotation operation is currently in progress.
	AuthRotationStateIdle = "idle"
	// AuthRotationStateInProgress means a key rotation operation is currently running.
	AuthRotationStateInProgress = "in_progress"
	// AuthRotationStateBlocked means rotation cannot proceed due to an unmanaged credential or incompatible client.
	AuthRotationStateBlocked = "blocked"
	// AuthRotationStateCompleted means rotation has completed successfully.
	AuthRotationStateCompleted = "completed"
	// AuthRotationStateFailed means rotation encountered an unrecoverable failure.
	AuthRotationStateFailed = "failed"
)

// Auth rotation stages corresponding to upstream migration procedure.
const (
	// AuthRotationStageNone means no stage is active.
	AuthRotationStageNone = ""
	// AuthRotationStageReadiness is the prerequisite stage checking cluster health and connectivity.
	AuthRotationStageReadiness = "readiness"
	// AuthRotationStagePrepareAuth allows requested cipher and sets auth_preferred_cipher.
	AuthRotationStagePrepareAuth = "prepare_auth"
	// AuthRotationStageRotateDaemons rotates monitor and service daemon credentials.
	AuthRotationStageRotateDaemons = "rotate_daemons"
	// AuthRotationStageActivateAndCheck restarts daemons and confirms service health.
	AuthRotationStageActivateAndCheck = "activate_and_check"
	// AuthRotationStageSwitchServiceAuth switches auth_service_cipher to the requested type.
	AuthRotationStageSwitchServiceAuth = "switch_service_auth"
	// AuthRotationStagePreventInsecureKeys sets mon_auth_allow_insecure_key=false.
	AuthRotationStagePreventInsecureKeys = "prevent_insecure_keys"
	// AuthRotationStageProtectAdmin rotates client.admin with backup credential safeguards.
	AuthRotationStageProtectAdmin = "protect_admin"
	// AuthRotationStageRotateClients rotates and distributes MicroCeph-managed client keys.
	AuthRotationStageRotateClients = "rotate_clients"
	// AuthRotationStageFinishSafely disallows insecure legacy types from auth_allowed_ciphers.
	AuthRotationStageFinishSafely = "finish_safely"
)

// AuthRotationRecord represents the persistent single-row record tracking CephX key rotation.
type AuthRotationRecord struct {
	TargetKeyType  string
	State          string
	Stage          string
	ClientName     string
	StepProgress   string
	Blocker        string
	Detail         string
	ApplyLockToken int64
}

// GetAuthRotation reads the single-row auth rotation record.
func GetAuthRotation(ctx context.Context, tx *sql.Tx) (*AuthRotationRecord, error) {
	var targetKeyType sql.NullString
	var state, stage string
	var clientName, stepProgress, blocker, detail sql.NullString
	var lockToken int64

	row := tx.QueryRowContext(ctx, `
SELECT coalesce(target_key_type, ''),
       state,
       stage,
       coalesce(client_name, ''),
       coalesce(step_progress, ''),
       coalesce(blocker, ''),
       coalesce(detail, ''),
       apply_lock_token
  FROM auth_rotation
 WHERE id = 1`)

	err := row.Scan(&targetKeyType, &state, &stage, &clientName, &stepProgress, &blocker, &detail, &lockToken)
	if err != nil {
		return nil, fmt.Errorf("failed to read auth rotation: %w", err)
	}

	return &AuthRotationRecord{
		TargetKeyType:  targetKeyType.String,
		State:          state,
		Stage:          stage,
		ClientName:     clientName.String,
		StepProgress:   stepProgress.String,
		Blocker:        blocker.String,
		Detail:         detail.String,
		ApplyLockToken: lockToken,
	}, nil
}

// SetAuthRotation updates the single-row auth rotation record.
func SetAuthRotation(ctx context.Context, tx *sql.Tx, rec AuthRotationRecord) error {
	result, err := tx.ExecContext(ctx, `
UPDATE auth_rotation
   SET target_key_type = ?,
       state = ?,
       stage = ?,
       client_name = ?,
       step_progress = ?,
       blocker = ?,
       detail = ?
 WHERE id = 1`,
		sqlNullStringFromStr(rec.TargetKeyType),
		rec.State,
		rec.Stage,
		sqlNullStringFromStr(rec.ClientName),
		sqlNullStringFromStr(rec.StepProgress),
		sqlNullStringFromStr(rec.Blocker),
		sqlNullStringFromStr(rec.Detail),
	)
	if err != nil {
		return fmt.Errorf("failed to update auth rotation: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("auth_rotation singleton row not found")
	}

	return nil
}

// ResetAuthRotation resets the auth rotation record back to the idle default state.
func ResetAuthRotation(ctx context.Context, tx *sql.Tx) error {
	result, err := tx.ExecContext(ctx, `
UPDATE auth_rotation
   SET target_key_type = NULL,
       state = 'idle',
       stage = '',
       client_name = NULL,
       step_progress = NULL,
       blocker = NULL,
       detail = NULL
 WHERE id = 1`)
	if err != nil {
		return fmt.Errorf("failed to reset auth rotation: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("auth_rotation singleton row not found")
	}

	return nil
}

// InitOrResumeAuthRotation initializes a new rotation or resumes an incomplete rotation.
// Returns the record and a boolean indicating whether the operation resumed an existing rotation (true)
// or started a fresh rotation (false).
func InitOrResumeAuthRotation(ctx context.Context, tx *sql.Tx, targetKeyType string, clientName string) (*AuthRotationRecord, bool, error) {
	rec, err := GetAuthRotation(ctx, tx)
	if err != nil {
		return nil, false, err
	}

	isIncomplete := rec.State == AuthRotationStateInProgress || rec.State == AuthRotationStateBlocked || rec.State == AuthRotationStateFailed
	if isIncomplete {
		if targetKeyType != "" && rec.TargetKeyType != "" && targetKeyType != rec.TargetKeyType {
			return nil, false, fmt.Errorf("an incomplete rotation to key type %q is in progress (stage: %q); cannot rotate to %q while incomplete", rec.TargetKeyType, rec.Stage, targetKeyType)
		}

		if clientName != rec.ClientName {
			return nil, false, fmt.Errorf("an incomplete rotation with client filter %q is in progress; cannot change client to %q while incomplete", rec.ClientName, clientName)
		}

		if rec.State == AuthRotationStateBlocked || rec.State == AuthRotationStateFailed {
			rec.State = AuthRotationStateInProgress
			rec.Blocker = ""
			rec.Detail = ""
			err = SetAuthRotation(ctx, tx, *rec)
			if err != nil {
				return nil, false, err
			}
		}

		return rec, true, nil
	}

	// Idle or completed: begin fresh rotation.
	newRec := &AuthRotationRecord{
		TargetKeyType: targetKeyType,
		State:         AuthRotationStateInProgress,
		Stage:         AuthRotationStageReadiness,
		ClientName:    clientName,
	}

	err = SetAuthRotation(ctx, tx, *newRec)
	if err != nil {
		return nil, false, err
	}

	return newRec, false, nil
}

// SetAuthRotationBlocker transitions the auth rotation state to blocked and records the blocker and detail message.
func SetAuthRotationBlocker(ctx context.Context, tx *sql.Tx, blocker string, detail string) error {
	result, err := tx.ExecContext(ctx, `
UPDATE auth_rotation
   SET state = ?,
       blocker = ?,
       detail = ?
 WHERE id = 1`, AuthRotationStateBlocked, sqlNullStringFromStr(blocker), sqlNullStringFromStr(detail))
	if err != nil {
		return fmt.Errorf("failed to set auth rotation blocker: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("auth_rotation singleton row not found")
	}

	return nil
}

// SetAuthRotationStage updates the current stage and progress payload of the rotation.
func SetAuthRotationStage(ctx context.Context, tx *sql.Tx, stage string, stepProgress string) error {
	result, err := tx.ExecContext(ctx, `
UPDATE auth_rotation
   SET stage = ?,
       step_progress = ?
 WHERE id = 1`, stage, sqlNullStringFromStr(stepProgress))
	if err != nil {
		return fmt.Errorf("failed to update auth rotation stage: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("auth_rotation singleton row not found")
	}

	return nil
}

// TryAcquireAuthRotationLock atomically acquires the cluster-wide auth rotation lock.
// token is the acquirer's unique identifier (e.g., acquisition timestamp in nanoseconds).
// staleBefore is the cutoff before which an existing token is considered expired and can be reclaimed.
func TryAcquireAuthRotationLock(ctx context.Context, tx *sql.Tx, token int64, staleBefore int64) (bool, error) {
	var prevToken int64
	err := tx.QueryRowContext(ctx, `SELECT apply_lock_token FROM auth_rotation WHERE id = 1`).Scan(&prevToken)
	if err != nil {
		return false, fmt.Errorf("failed to read auth rotation apply lock token: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
UPDATE auth_rotation
   SET apply_lock_token = ?
 WHERE id = 1 AND (apply_lock_token = 0 OR apply_lock_token < ?)`, token, staleBefore)
	if err != nil {
		return false, fmt.Errorf("failed to acquire auth rotation apply lock: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rows == 1 {
		if prevToken != 0 {
			logger.Warnf("auth rotation apply lock reclaimed from abandoned holder (held %ds); a prior operation likely crashed mid-rotation", (token-prevToken)/int64(1e9))
		}
		logger.Debugf("auth rotation apply lock acquired (token=%d, reclaimed=%v)", token, prevToken != 0)
		return true, nil
	}

	return false, nil
}

// ReleaseAuthRotationLock releases the auth rotation lock held by the given token.
func ReleaseAuthRotationLock(ctx context.Context, tx *sql.Tx, token int64) (bool, error) {
	result, err := tx.ExecContext(ctx, `
UPDATE auth_rotation SET apply_lock_token = 0 WHERE id = 1 AND apply_lock_token = ?`, token)
	if err != nil {
		return false, fmt.Errorf("failed to release auth rotation apply lock: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to check rows affected: %w", err)
	}

	return rows == 1, nil
}

func sqlNullStringFromStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
