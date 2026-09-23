package ceph

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

func managedSMBTestState(t *testing.T, name string) *mocks.StateInterface {
	t.Helper()
	state := mocks.NewStateInterface(t)
	state.On("ClusterState").Return(&mocks.MockState{ClusterName: name}).Maybe()
	return state
}

func TestEnableManagedSMBCreatesFirstMember(t *testing.T) {
	originalEnsure := ensureManagedSMBBackendFunc
	originalMembers := getManagedSMBMembersFunc
	originalCreate := createManagedSMBClusterFunc
	t.Cleanup(func() {
		ensureManagedSMBBackendFunc = originalEnsure
		getManagedSMBMembersFunc = originalMembers
		createManagedSMBClusterFunc = originalCreate
	})
	events := []string{}
	ensureManagedSMBBackendFunc = func(_ context.Context) error {
		events = append(events, "ensure backend")
		return nil
	}
	getManagedSMBMembersFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) ([]string, error) {
		events = append(events, "list members")
		return nil, nil
	}
	var createdTargets []string
	var createdRequest types.ManagedSMBService
	createManagedSMBClusterFunc = func(request types.ManagedSMBService, targets []string) error {
		events = append(events, "create cluster")
		createdRequest = request
		createdTargets = append(createdTargets, targets...)
		return nil
	}
	request := types.ManagedSMBService{
		ClusterID:      "files",
		DefineUserPass: []string{"smbuser%secret"},
		BindNetworks:   []string{"192.0.2.0/24"},
		Port:           1445,
	}

	err := EnableManagedSMB(context.Background(), managedSMBTestState(t, "node-a"), request)

	require.NoError(t, err)
	assert.Equal(t, request, createdRequest)
	assert.Equal(t, []string{"node-a"}, createdTargets)
	assert.Equal(t, []string{"list members", "ensure backend", "create cluster"}, events)
}

func TestEnableManagedSMBRejectsFirstMemberWithoutUserConfigBeforePreparingBackend(t *testing.T) {
	originalEnsure := ensureManagedSMBBackendFunc
	originalMembers := getManagedSMBMembersFunc
	originalLoad := loadManagedSMBClusterFunc
	t.Cleanup(func() {
		ensureManagedSMBBackendFunc = originalEnsure
		getManagedSMBMembersFunc = originalMembers
		loadManagedSMBClusterFunc = originalLoad
	})
	getManagedSMBMembersFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) ([]string, error) {
		return nil, nil
	}
	loadManagedSMBClusterFunc = func(_ string) (map[string]any, error) {
		return nil, errors.New("not found")
	}
	ensureCalls := 0
	ensureManagedSMBBackendFunc = func(_ context.Context) error {
		ensureCalls++
		return nil
	}

	err := EnableManagedSMB(
		context.Background(),
		managedSMBTestState(t, "node-a"),
		types.ManagedSMBService{ClusterID: "files"},
	)

	assert.ErrorContains(t, err, "requires a user or user-group resource")
	assert.Zero(t, ensureCalls)
}

func TestEnableManagedSMBReconcilesExistingClusterBeforeFirstCallback(t *testing.T) {
	originalMembers := getManagedSMBMembersFunc
	originalLoad := loadManagedSMBClusterFunc
	originalApply := applyManagedSMBClusterFunc
	t.Cleanup(func() {
		getManagedSMBMembersFunc = originalMembers
		loadManagedSMBClusterFunc = originalLoad
		applyManagedSMBClusterFunc = originalApply
	})
	getManagedSMBMembersFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) ([]string, error) {
		return nil, nil
	}
	loadManagedSMBClusterFunc = func(_ string) (map[string]any, error) {
		return map[string]any{
			"resource_type": "ceph.smb.cluster",
			"cluster_id":    "files",
			"auth_mode":     "user",
			"placement": map[string]any{
				"hosts": []any{"node-a"},
				"count": float64(1),
			},
		}, nil
	}
	var applied map[string]any
	applyManagedSMBClusterFunc = func(resource map[string]any) error {
		applied = resource
		return nil
	}

	err := EnableManagedSMB(
		context.Background(),
		managedSMBTestState(t, "node-b"),
		types.ManagedSMBService{ClusterID: "files"},
	)

	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"hosts": []string{"node-a", "node-b"},
		"count": 2,
	}, applied["placement"])
}

func TestEnableManagedSMBReconcilesAdditionalMemberAndClusterOptions(t *testing.T) {
	originalMembers := getManagedSMBMembersFunc
	originalLoad := loadManagedSMBClusterFunc
	originalApply := applyManagedSMBClusterFunc
	t.Cleanup(func() {
		getManagedSMBMembersFunc = originalMembers
		loadManagedSMBClusterFunc = originalLoad
		applyManagedSMBClusterFunc = originalApply
	})
	getManagedSMBMembersFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) ([]string, error) {
		return []string{"node-b", "node-a"}, nil
	}
	loadManagedSMBClusterFunc = func(_ string) (map[string]any, error) {
		return map[string]any{
			"resource_type": "ceph.smb.cluster",
			"cluster_id":    "files",
			"auth_mode":     "user",
			"placement": map[string]any{
				"hosts": []any{"node-a", "node-b"},
				"count": float64(2),
			},
		}, nil
	}
	var applied map[string]any
	applyManagedSMBClusterFunc = func(resource map[string]any) error {
		applied = resource
		return nil
	}
	request := types.ManagedSMBService{
		ClusterID:     "files",
		BindAddresses: []string{"192.0.2.10", "192.0.2.11"},
		Port:          1445,
	}

	err := EnableManagedSMB(context.Background(), managedSMBTestState(t, "node-c"), request)

	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"hosts": []string{"node-a", "node-b", "node-c"},
		"count": 3,
	}, applied["placement"])
	assert.Equal(t, []map[string]string{
		{"address": "192.0.2.10"},
		{"address": "192.0.2.11"},
	}, applied["bind_addrs"])
	assert.Equal(t, map[string]int{"smb": 1445}, applied["custom_ports"])
}

func TestEnableManagedSMBUnchangedMemberIsNoOp(t *testing.T) {
	originalMembers := getManagedSMBMembersFunc
	originalLoad := loadManagedSMBClusterFunc
	originalApply := applyManagedSMBClusterFunc
	t.Cleanup(func() {
		getManagedSMBMembersFunc = originalMembers
		loadManagedSMBClusterFunc = originalLoad
		applyManagedSMBClusterFunc = originalApply
	})
	getManagedSMBMembersFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) ([]string, error) {
		return []string{"node-a"}, nil
	}
	loadCalls := 0
	applyCalls := 0
	loadManagedSMBClusterFunc = func(_ string) (map[string]any, error) {
		loadCalls++
		return map[string]any{}, nil
	}
	applyManagedSMBClusterFunc = func(_ map[string]any) error {
		applyCalls++
		return nil
	}

	err := EnableManagedSMB(
		context.Background(),
		managedSMBTestState(t, "node-a"),
		types.ManagedSMBService{ClusterID: "files"},
	)

	require.NoError(t, err)
	assert.Zero(t, loadCalls)
	assert.Zero(t, applyCalls)
}

func TestEnsureManagedSMBBackendEnablesModulesAndOrchestrator(t *testing.T) {
	runner := mocks.NewRunner(t)
	runner.On("RunCommand", "ceph", "mgr", "module", "enable", "microceph").Return("ok", nil).Once()
	runner.On("RunCommand", "ceph", "orch", "set", "backend", "microceph").Return("ok", nil).Once()
	runner.On("RunCommand", "ceph", "mgr", "module", "enable", "smb").Return("ok", nil).Once()
	originalRunner := common.ProcessExec
	t.Cleanup(func() {
		common.ProcessExec = originalRunner
	})
	common.ProcessExec = runner

	err := ensureManagedSMBBackend(context.Background())

	assert.NoError(t, err)
}

func TestCreateManagedSMBClusterPassesSupportedOptions(t *testing.T) {
	runner := mocks.NewRunner(t)
	runner.On(
		"RunCommand",
		"ceph",
		"smb", "cluster", "create", "files", "user",
		"--placement", "1 node-a",
		"--user-group-ref", "existing-users",
		"--define-user-pass", "smbuser%secret",
	).Return("ok", nil).Once()
	originalRunner := common.ProcessExec
	t.Cleanup(func() {
		common.ProcessExec = originalRunner
	})
	common.ProcessExec = runner
	request := types.ManagedSMBService{
		ClusterID:      "files",
		DefineUserPass: []string{"smbuser%secret"},
		UserGroupRefs:  []string{"existing-users"},
	}

	err := createManagedSMBCluster(request, []string{"node-a"})

	assert.NoError(t, err)
}

func TestDisableManagedSMBReconcilesRemainingMembers(t *testing.T) {
	originalMembers := getManagedSMBMembersFunc
	originalLoad := loadManagedSMBClusterFunc
	originalApply := applyManagedSMBClusterFunc
	t.Cleanup(func() {
		getManagedSMBMembersFunc = originalMembers
		loadManagedSMBClusterFunc = originalLoad
		applyManagedSMBClusterFunc = originalApply
	})
	getManagedSMBMembersFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) ([]string, error) {
		return []string{"node-a", "node-b", "node-c"}, nil
	}
	loadManagedSMBClusterFunc = func(_ string) (map[string]any, error) {
		return map[string]any{
			"resource_type": "ceph.smb.cluster",
			"cluster_id":    "files",
			"auth_mode":     "user",
		}, nil
	}
	var applied map[string]any
	applyManagedSMBClusterFunc = func(resource map[string]any) error {
		applied = resource
		return nil
	}

	err := DisableManagedSMB(context.Background(), managedSMBTestState(t, "node-b"), "files")

	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"hosts": []string{"node-a", "node-c"},
		"count": 2,
	}, applied["placement"])
}

func TestDisableManagedSMBRemovesFinalMember(t *testing.T) {
	originalMembers := getManagedSMBMembersFunc
	originalRemove := removeManagedSMBClusterFunc
	t.Cleanup(func() {
		getManagedSMBMembersFunc = originalMembers
		removeManagedSMBClusterFunc = originalRemove
	})
	getManagedSMBMembersFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) ([]string, error) {
		return []string{"node-a"}, nil
	}
	removed := ""
	removeManagedSMBClusterFunc = func(clusterID string) error {
		removed = clusterID
		return nil
	}

	err := DisableManagedSMB(context.Background(), managedSMBTestState(t, "node-a"), "files")

	require.NoError(t, err)
	assert.Equal(t, "files", removed)
}
