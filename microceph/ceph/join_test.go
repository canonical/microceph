package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/canonical/microceph/microceph/database"
	"github.com/stretchr/testify/assert"
)

// mockJoinHostTagStore tracks host tags in memory for join testing.
type mockJoinHostTagStore struct {
	tags []database.HostTag
}

func newMockJoinHostTagStore(tags ...database.HostTag) *mockJoinHostTagStore {
	return &mockJoinHostTagStore{tags: tags}
}

func (m *mockJoinHostTagStore) getAll(_ context.Context, _ *sql.Tx, _ ...database.HostTagFilter) ([]database.HostTag, error) {
	return m.tags, nil
}

func (m *mockJoinHostTagStore) create(_ context.Context, _ *sql.Tx, object database.HostTag) (int64, error) {
	for _, tag := range m.tags {
		if tag.Member == object.Member && tag.Key == object.Key {
			return -1, fmt.Errorf("duplicate key")
		}
	}
	m.tags = append(m.tags, object)
	return int64(len(m.tags)), nil
}

// withMockJoinStore swaps the package-level DB functions for mock versions,
// returning a restore function.
func withMockJoinStore(store *mockJoinHostTagStore) func() {
	origGetAll := getHostTags
	origCreate := joinCreateHostTag
	getHostTags = store.getAll
	joinCreateHostTag = store.create
	return func() {
		getHostTags = origGetAll
		joinCreateHostTag = origCreate
	}
}

func TestValidateAndSetJoinAZSuccess(t *testing.T) {
	// Existing nodes have AZs, joining with an AZ should succeed.
	store := newMockJoinHostTagStore(
		database.HostTag{Member: "node0", Key: "availability-zone", Value: "az-0"},
	)
	defer withMockJoinStore(store)() // Revert back to pre-mock

	err := validateAndSetJoinAZ(context.Background(), nil, "node1", "az-1")
	assert.NoError(t, err)

	// Check the new entry was created.
	found := false
	for _, tag := range store.tags {
		if tag.Member == "node1" && tag.Key == "availability-zone" && tag.Value == "az-1" {
			found = true
		}
	}
	assert.True(t, found, "expected availability-zone host tag for node1 to be created")
}

func TestValidateAndSetJoinAZNoAZWhenNoneExist(t *testing.T) {
	// No existing AZs, joining without an AZ should succeed (no-op).
	store := newMockJoinHostTagStore()
	defer withMockJoinStore(store)()

	err := validateAndSetJoinAZ(context.Background(), nil, "node1", "")
	assert.NoError(t, err)
	assert.Len(t, store.tags, 0, "no host tags should be created")
}

func TestValidateAndSetJoinAZRejectsEmptyWhenOthersSet(t *testing.T) {
	// Existing nodes have AZs, joining without an AZ should fail.
	store := newMockJoinHostTagStore(
		database.HostTag{Member: "node0", Key: "availability-zone", Value: "az-0"},
	)
	defer withMockJoinStore(store)()

	err := validateAndSetJoinAZ(context.Background(), nil, "node1", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mixed empty availability zones")
	assert.Contains(t, err.Error(), "without an associated availability zone")
}

func TestValidateAndSetJoinAZRejectsSetWhenOthersEmpty(t *testing.T) {
	// No existing AZs, joining with an AZ should fail.
	// The mock returns no tags (empty store), simulating hosts without AZs.
	store := newMockJoinHostTagStore()
	defer withMockJoinStore(store)()

	err := validateAndSetJoinAZ(context.Background(), nil, "node1", "az-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mixed empty availability zones")
	assert.Contains(t, err.Error(), "existing hosts do not have an availability zone")
}

func TestValidateAndSetJoinAZRejectsInvalidName(t *testing.T) {
	store := newMockJoinHostTagStore(
		database.HostTag{Member: "node0", Key: "availability-zone", Value: "az-0"},
	)
	defer withMockJoinStore(store)()

	// Spaces are not valid CRUSH bucket names.
	err := validateAndSetJoinAZ(context.Background(), nil, "node1", "az 1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid availability zone name")

	// Slashes are not valid.
	err = validateAndSetJoinAZ(context.Background(), nil, "node1", "az/1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid availability zone name")
}
