package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/microceph/microceph/logger"
)

// PlacementPolicyRecord is the single-row record tracking the active role-managed
// declarative placement policy (CE142). PolicyJSON holds the last accepted
// placement policy as a JSON blob; Active records whether a role-managed
// placement policy is currently in effect. LastRefusal holds the most recent
// placement refusal reason (e.g. keep-one invariant) so operators and charms
// polling GET /1.0/placement can inspect why the last PUT was rejected.
type PlacementPolicyRecord struct {
	Active      bool
	PolicyJSON  string
	LastRefusal string
}

// GetPlacementPolicy reads the single-row placement policy record.
func GetPlacementPolicy(ctx context.Context, tx *sql.Tx) (*PlacementPolicyRecord, error) {
	var active int64
	var policyJSON string
	var lastRefusal sql.NullString
	row := tx.QueryRowContext(ctx, `
SELECT active, coalesce(policy_json, ''), last_refusal FROM placement_policy WHERE id = 1`)
	err := row.Scan(&active, &policyJSON, &lastRefusal)
	if err != nil {
		return nil, fmt.Errorf("failed to read placement policy: %w", err)
	}
	return &PlacementPolicyRecord{Active: active != 0, PolicyJSON: policyJSON, LastRefusal: lastRefusal.String}, nil
}

// SetPlacementPolicy updates the single-row placement policy record (the row
// is created by the schema migration; a missing row is an error).
func SetPlacementPolicy(ctx context.Context, tx *sql.Tx, active bool, policyJSON string) error {
	a := 0
	if active {
		a = 1
	}
	result, err := tx.ExecContext(ctx, `
UPDATE placement_policy SET active = ?, policy_json = ? WHERE id = 1`, a, policyJSON)
	if err != nil {
		return fmt.Errorf("failed to update placement policy: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("placement_policy singleton row not found")
	}
	return nil
}

// ClearPlacementPolicy clears the active role-managed placement policy without
// touching services. It sets Active to false and clears the stored policy JSON
// and the last refusal reason.
func ClearPlacementPolicy(ctx context.Context, tx *sql.Tx) error {
	result, err := tx.ExecContext(ctx, `UPDATE placement_policy SET active = 0, policy_json = NULL, last_refusal = NULL WHERE id = 1`)
	if err != nil {
		return fmt.Errorf("failed to clear placement policy: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("placement_policy singleton row not found")
	}
	return nil
}

// TryAcquirePlacementApplyLock atomically acquires the cluster-wide placement
// apply lock via a conditional UPDATE on the placement_policy singleton row.
// token is the acquirer's unique identifier, its acquisition time in Unix
// nanoseconds, so the token doubles as a lease timestamp. staleBefore is the
// oldest acquisition time still considered live: a held lock whose token is
// older is treated as abandoned by a crashed daemon and is reclaimed. Returns
// true when the lock was acquired (including by stale reclaim), false when a
// live holder exists.
//
// The conditional UPDATE executes atomically in dqlite, so two members racing
// to acquire cannot both succeed — this mirrors the atomicStartBootstrap CAS.
// Clock skew between members shortens or extends the lease by the skew; the
// lease is sized in minutes so ordinary NTP-level skew is harmless.
func TryAcquirePlacementApplyLock(ctx context.Context, tx *sql.Tx, token int64, staleBefore int64) (bool, error) {
	// Read the existing token BEFORE the conditional UPDATE so a stale-reclaim
	// can be distinguished from a clean acquire in logs. The read and the
	// conditional UPDATE run in the same transaction, so they observe a
	// consistent snapshot.
	var prevToken int64
	err := tx.QueryRowContext(ctx, `SELECT apply_lock_token FROM placement_policy WHERE id = 1`).Scan(&prevToken)
	if err != nil {
		return false, fmt.Errorf("failed to read placement apply lock token: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
UPDATE placement_policy
   SET apply_lock_token = ?
 WHERE id = 1 AND (apply_lock_token = 0 OR apply_lock_token < ?)`, token, staleBefore)
	if err != nil {
		return false, fmt.Errorf("failed to acquire placement apply lock: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 1 {
		if prevToken != 0 {
			// Stale reclaim: a prior holder's lease expired (its daemon likely
			// crashed mid-apply). Warn so operators can correlate a clobbered
			// apply with the reclaim event.
			logger.Warnf("placement apply lock reclaimed from abandoned holder (held %ds); a prior apply likely crashed mid-operation", (token-prevToken)/int64(1e9))
		}
		logger.Debugf("placement apply lock acquired (token=%d, reclaimed=%v)", token, prevToken != 0)
		return true, nil
	}
	return false, nil
}

// ReleasePlacementApplyLock releases the placement apply lock, but only when
// it is still held by token: if the holder's lease expired mid-apply and
// another writer reclaimed the lock, the reclaimer's lock must not be
// clobbered. Returns true when the lock was released, false when it was no
// longer held by token (already reclaimed or released).
func ReleasePlacementApplyLock(ctx context.Context, tx *sql.Tx, token int64) (bool, error) {
	result, err := tx.ExecContext(ctx, `
UPDATE placement_policy SET apply_lock_token = 0 WHERE id = 1 AND apply_lock_token = ?`, token)
	if err != nil {
		return false, fmt.Errorf("failed to release placement apply lock: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to check rows affected: %w", err)
	}
	return rows == 1, nil
}

// SetPlacementRefusal persists the most recent placement refusal reason so
// operators and charms polling GET /1.0/placement can inspect why the last
// placement PUT was rejected. An empty reason clears the field.
func SetPlacementRefusal(ctx context.Context, tx *sql.Tx, reason string) error {
	var val interface{}
	if reason == "" {
		val = nil
	} else {
		val = reason
	}
	result, err := tx.ExecContext(ctx, `UPDATE placement_policy SET last_refusal = ? WHERE id = 1`, val)
	if err != nil {
		return fmt.Errorf("failed to set placement refusal: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("placement_policy singleton row not found")
	}
	return nil
}
