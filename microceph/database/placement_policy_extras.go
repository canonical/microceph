package database

import (
	"context"
	"database/sql"
	"fmt"
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

// SetPlacementPolicy upserts the single-row placement policy record.
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
