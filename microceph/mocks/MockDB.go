package mocks

import (
	"context"
	"database/sql"

	dqliteClient "github.com/canonical/go-dqlite/v3/client"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
)

// MockDB is a minimal mock for mcTypes.DB that only implements Transaction.
// All other methods are no-ops or return zero values. It is intended for unit
// tests that need to exercise code paths that call Database().Transaction().
type MockDB struct {
	mcTypes.DB

	TxFn func(ctx context.Context, f func(context.Context, *sql.Tx) error) error
}

// Transaction delegates to the configured TxFn, or returns nil if unset.
func (m *MockDB) Transaction(ctx context.Context, f func(context.Context, *sql.Tx) error) error {
	if m.TxFn == nil {
		return nil
	}
	return m.TxFn(ctx, f)
}

// Leader returns nil, nil (not used in unit tests).
func (m *MockDB) Leader(_ context.Context) (*dqliteClient.Client, error) {
	return nil, nil
}

// Cluster returns nil, nil (not used in unit tests).
func (m *MockDB) Cluster(_ context.Context, _ *dqliteClient.Client) ([]dqliteClient.NodeInfo, error) {
	return nil, nil
}

// Status returns empty string (not used in unit tests).
func (m *MockDB) Status() mcTypes.DatabaseStatus {
	return ""
}

// IsOpen returns nil (not used in unit tests).
func (m *MockDB) IsOpen(_ context.Context) error {
	return nil
}

// SchemaVersion returns zeros (not used in unit tests).
func (m *MockDB) SchemaVersion() (uint64, uint64, mcTypes.Extensions) {
	return 0, 0, nil
}
