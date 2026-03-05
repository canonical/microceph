package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/canonical/microceph/microceph/database"
	"github.com/stretchr/testify/assert"
)

// mockJoinConfigStore tracks config items in memory for join testing.
type mockJoinConfigStore struct {
	items []database.ConfigItem
}

func newMockJoinConfigStore(items ...database.ConfigItem) *mockJoinConfigStore {
	return &mockJoinConfigStore{items: items}
}

func (m *mockJoinConfigStore) getAll(_ context.Context, _ *sql.Tx, _ ...database.ConfigItemFilter) ([]database.ConfigItem, error) {
	return m.items, nil
}

func (m *mockJoinConfigStore) create(_ context.Context, _ *sql.Tx, object database.ConfigItem) (int64, error) {
	for _, item := range m.items {
		if item.Key == object.Key {
			return -1, fmt.Errorf("duplicate key")
		}
	}
	m.items = append(m.items, object)
	return int64(len(m.items)), nil
}

// withMockJoinStore swaps the package-level DB functions for mock versions,
// returning a restore function.
func withMockJoinStore(store *mockJoinConfigStore) func() {
	origGetAll := getConfigItems
	origCreate := joinCreateConfigItem
	getConfigItems = store.getAll
	joinCreateConfigItem = store.create
	return func() {
		getConfigItems = origGetAll
		joinCreateConfigItem = origCreate
	}
}

func TestValidateAndSetJoinAZSuccess(t *testing.T) {
	// Existing nodes have AZs, joining with an AZ should succeed.
	store := newMockJoinConfigStore(
		database.ConfigItem{Key: "az.host.node0", Value: "az-0"},
	)
	defer withMockJoinStore(store)() // Revert back to pre-mock

	err := validateAndSetJoinAZ(context.Background(), nil, "node1", "az-1")
	assert.NoError(t, err)

	// Check the new entry was created.
	found := false
	for _, item := range store.items {
		if item.Key == "az.host.node1" && item.Value == "az-1" {
			found = true
		}
	}
	assert.True(t, found, "expected az.host.node1 to be created")
}

func TestValidateAndSetJoinAZNoAZWhenNoneExist(t *testing.T) {
	// No existing AZs, joining without an AZ should succeed (no-op).
	store := newMockJoinConfigStore()
	defer withMockJoinStore(store)()

	err := validateAndSetJoinAZ(context.Background(), nil, "node1", "")
	assert.NoError(t, err)
	assert.Len(t, store.items, 0, "no config items should be created")
}

func TestValidateAndSetJoinAZRejectsEmptyWhenOthersSet(t *testing.T) {
	// Existing nodes have AZs, joining without an AZ should fail.
	store := newMockJoinConfigStore(
		database.ConfigItem{Key: "az.host.node0", Value: "az-0"},
	)
	defer withMockJoinStore(store)()

	err := validateAndSetJoinAZ(context.Background(), nil, "node1", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mixed empty availability zones")
	assert.Contains(t, err.Error(), "without an associated availability zone")
}

func TestValidateAndSetJoinAZRejectsSetWhenOthersEmpty(t *testing.T) {
	// No existing AZs, joining with an AZ should fail.
	store := newMockJoinConfigStore(
		database.ConfigItem{Key: "fsid", Value: "some-fsid"},            // non-AZ config
		database.ConfigItem{Key: "public_network", Value: "10.0.0.0/8"}, // non-AZ config
	)
	defer withMockJoinStore(store)()

	err := validateAndSetJoinAZ(context.Background(), nil, "node1", "az-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mixed empty availability zones")
	assert.Contains(t, err.Error(), "existing hosts do not have an availability zone")
}
