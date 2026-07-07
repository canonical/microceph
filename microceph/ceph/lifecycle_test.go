package ceph

import (
	"context"
	"testing"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/assert"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
)

// newLifecycleTestState builds a MockState wired to a mockLifecycleDB so the
// lifecycle/config helpers can be exercised against a real in-memory SQLite.
func newLifecycleTestState(t *testing.T, mockDB *mockLifecycleDB) (*mocks.StateInterface, *mocks.MockState) {
	t.Helper()
	si := mocks.NewStateInterface(t)
	u := api.NewURL()
	u.Host("1.1.1.1")
	state := &mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
		Cert:        &shared.CertInfo{},
		DBObj:       &mocks.MockDB{TxFn: mockDB.Transaction},
	}
	si.On("ClusterState").Return(state).Maybe()
	return si, state
}

// TestMarkCephBootstrappedFunc verifies MarkCephBootstrappedFunc records the
// lifecycle row as bootstrapped (CE142: fresh non-deferred bootstrappers call
// this so a newly-created cluster is not left not_bootstrapped).
func TestMarkCephBootstrappedFunc(t *testing.T) {
	mockDB := newMockLifecycleDB()
	si, _ := newLifecycleTestState(t, mockDB)

	err := MarkCephBootstrappedFunc(context.Background(), si)
	assert.NoError(t, err)

	lc := mockDB.get()
	assert.Equal(t, database.CephStateBootstrapped, lc.CephBootstrapState)
}

// TestCephIsBootstrappedLifecycleTrue verifies the lifecycle row is the primary
// signal: when it says bootstrapped, cephIsBootstrappedFunc returns true without
// needing config rows.
func TestCephIsBootstrappedLifecycleTrue(t *testing.T) {
	mockDB := newMockLifecycleDB()
	mockDB.set(database.ClusterLifecycle{CephBootstrapState: database.CephStateBootstrapped})
	si, _ := newLifecycleTestState(t, mockDB)

	got, err := cephIsBootstrappedFunc(context.Background(), si)
	assert.NoError(t, err)
	assert.True(t, got)
}

// TestCephIsBootstrappedConfigFallback verifies the defensive fallback: when
// the lifecycle row is stale (not_bootstrapped) but fsid + admin keyring config
// rows exist, cephIsBootstrappedFunc returns true so placement is not rejected on a
// fresh non-deferred cluster whose lifecycle row was not marked.
func TestCephIsBootstrappedConfigFallback(t *testing.T) {
	mockDB := newMockLifecycleDB()
	// Lifecycle stale: not_bootstrapped (default), but config rows exist.
	mockDB.setConfig(map[string]string{
		"fsid":                 "deadbeef-0000-0000-0000-000000000000",
		"keyring.client.admin": "AQABfakekey==",
	})
	si, _ := newLifecycleTestState(t, mockDB)

	got, err := cephIsBootstrappedFunc(context.Background(), si)
	assert.NoError(t, err)
	assert.True(t, got, "config rows must defensively indicate bootstrapped")
}

// TestCephIsBootstrappedNeither verifies that with neither a bootstrapped
// lifecycle row nor config rows, cephIsBootstrappedFunc returns false.
func TestCephIsBootstrappedNeither(t *testing.T) {
	mockDB := newMockLifecycleDB() // not_bootstrapped, no config rows
	si, _ := newLifecycleTestState(t, mockDB)

	got, err := cephIsBootstrappedFunc(context.Background(), si)
	assert.NoError(t, err)
	assert.False(t, got)
}
