package ceph

import (
	"context"
	"errors"
	"testing"

	"github.com/canonical/microceph/microceph/interfaces"
	microCluster "github.com/canonical/microcluster/v2/cluster"
	"github.com/stretchr/testify/assert"
)

func TestResolveRemovalAddresses(t *testing.T) {
	tests := []struct {
		name              string
		memberName        string
		trustStoreAddress string
		members           []microCluster.CoreClusterMember
		expectDBAddress   string
		expectDqliteAddr  string
		expectFound       bool
	}{
		{
			name:              "prefer database address when trust-store differs",
			memberName:        "node2",
			trustStoreAddress: "10.0.0.99:7443",
			members: []microCluster.CoreClusterMember{
				{Name: "node1", Address: "10.0.0.1:7443"},
				{Name: "node2", Address: "10.0.0.2:7443"},
			},
			expectDBAddress:  "10.0.0.2:7443",
			expectDqliteAddr: "10.0.0.2:7443",
			expectFound:      true,
		},
		{
			name:              "fallback to trust-store address when member absent from db",
			memberName:        "node3",
			trustStoreAddress: "10.0.0.3:7443",
			members: []microCluster.CoreClusterMember{
				{Name: "node1", Address: "10.0.0.1:7443"},
				{Name: "node2", Address: "10.0.0.2:7443"},
			},
			expectDBAddress:  "",
			expectDqliteAddr: "10.0.0.3:7443",
			expectFound:      false,
		},
		{
			name:              "do not fallback to trust-store address if it belongs to another member",
			memberName:        "node3",
			trustStoreAddress: "10.0.0.2:7443",
			members: []microCluster.CoreClusterMember{
				{Name: "node1", Address: "10.0.0.1:7443"},
				{Name: "node2", Address: "10.0.0.2:7443"},
			},
			expectDBAddress:  "",
			expectDqliteAddr: "",
			expectFound:      false,
		},
		{
			name:              "missing from both db and trust-store",
			memberName:        "node3",
			trustStoreAddress: "",
			members: []microCluster.CoreClusterMember{
				{Name: "node1", Address: "10.0.0.1:7443"},
			},
			expectDBAddress:  "",
			expectDqliteAddr: "",
			expectFound:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbAddr, dqliteAddr, found := resolveRemovalAddresses(tt.memberName, tt.members, tt.trustStoreAddress)
			assert.Equal(t, tt.expectDBAddress, dbAddr)
			assert.Equal(t, tt.expectDqliteAddr, dqliteAddr)
			assert.Equal(t, tt.expectFound, found)
		})
	}
}

func TestShouldReportForceRemoveNotFound(t *testing.T) {
	assert.True(t, shouldReportForceRemoveNotFound(false, false, false))
	assert.False(t, shouldReportForceRemoveNotFound(true, false, false))
	assert.False(t, shouldReportForceRemoveNotFound(false, true, false))
	assert.False(t, shouldReportForceRemoveNotFound(false, false, true))
}

func TestEnsureRemovalLeavesCluster(t *testing.T) {
	tests := []struct {
		name      string
		member    string
		members   []microCluster.CoreClusterMember
		wantError bool
	}{
		{
			name:   "target present with one remaining non-pending member",
			member: "node2",
			members: []microCluster.CoreClusterMember{
				{Name: "node1", Role: ""},
				{Name: "node2", Role: ""},
			},
			wantError: false,
		},
		{
			name:   "target present and no remaining non-pending members",
			member: "node1",
			members: []microCluster.CoreClusterMember{
				{Name: "node1", Role: ""},
				{Name: "node2", Role: microCluster.Pending},
			},
			wantError: true,
		},
		{
			name:   "target missing treated as cleanup only",
			member: "missing-node",
			members: []microCluster.CoreClusterMember{
				{Name: "node1", Role: ""},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ensureRemovalLeavesCluster(tt.member, tt.members)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestReconcileAfterForceRemoveContinuesAfterErrors(t *testing.T) {
	originalSyncTrustStore := syncTrustStoreFromDatabaseOp
	originalReconcileMonHostEntries := reconcileMonHostEntriesOp
	originalUpdateConfig := updateConfigOp
	originalSyncClusterRemotesOnPeers := syncClusterRemotesOnPeersOp
	defer func() {
		syncTrustStoreFromDatabaseOp = originalSyncTrustStore
		reconcileMonHostEntriesOp = originalReconcileMonHostEntries
		updateConfigOp = originalUpdateConfig
		syncClusterRemotesOnPeersOp = originalSyncClusterRemotesOnPeers
	}()

	calls := []string{}
	syncTrustStoreFromDatabaseOp = func(ctx context.Context, s interfaces.StateInterface) error {
		calls = append(calls, "sync-trust-store")
		return errors.New("trust-store unavailable")
	}
	reconcileMonHostEntriesOp = func(ctx context.Context, s interfaces.StateInterface) (int, error) {
		calls = append(calls, "reconcile-mon-host")
		return 0, errors.New("config database unavailable")
	}
	updateConfigOp = func(ctx context.Context, s interfaces.StateInterface) error {
		calls = append(calls, "update-config")
		return nil
	}
	syncClusterRemotesOnPeersOp = func(ctx context.Context, s interfaces.StateInterface) {
		calls = append(calls, "sync-peers")
	}

	err := reconcileAfterForceRemove(context.Background(), nil)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "failed to refresh trust-store from database")
	assert.ErrorContains(t, err, "failed to reconcile monitor host entries")
	assert.Equal(t, []string{"sync-trust-store", "reconcile-mon-host", "update-config", "sync-peers"}, calls)
}

func TestWrapForceRemoveOutcomeError(t *testing.T) {
	t.Run("no errors returns nil", func(t *testing.T) {
		err := wrapForceRemoveOutcomeError("node2", nil, nil)
		assert.NoError(t, err)
	})

	t.Run("partial failures include top-level context and causes", func(t *testing.T) {
		dqliteErr := errors.New("failed to remove member \"node2\" from dqlite: timeout")
		reconcileErr := errors.New("failed to refresh trust-store from database: unavailable")

		err := wrapForceRemoveOutcomeError("node2", dqliteErr, reconcileErr)
		assert.Error(t, err)
		assert.ErrorContains(t, err, "force remove \"node2\" completed with partial failures")
		assert.ErrorContains(t, err, "from dqlite")
		assert.ErrorContains(t, err, "refresh trust-store")
	})
}
