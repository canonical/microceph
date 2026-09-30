package ceph

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

func preserveManagedSMBFuncs(t *testing.T) {
	t.Helper()
	ensure, load, create := ensureManagedSMBBackendFunc, loadManagedSMBClusterFunc, createManagedSMBClusterFunc
	apply, remove := applyManagedSMBClusterFunc, removeManagedSMBClusterFunc
	cleanup := disableSMBLocalFunc
	disableSMBLocalFunc = func(context.Context, interfaces.StateInterface, string) error { return nil }
	t.Cleanup(func() {
		ensureManagedSMBBackendFunc, loadManagedSMBClusterFunc, createManagedSMBClusterFunc = ensure, load, create
		applyManagedSMBClusterFunc, removeManagedSMBClusterFunc = apply, remove
		disableSMBLocalFunc = cleanup
	})
}

func managedResource(hosts ...string) map[string]any {
	entries := make([]any, len(hosts))
	for i, host := range hosts {
		entries[i] = host
	}
	return map[string]any{
		"resource_type": "ceph.smb.cluster", "cluster_id": "files", "auth_mode": "user", "clustering": "always",
		"placement": map[string]any{"hosts": entries, "count": float64(len(hosts))},
	}
}

func TestEnableManagedSMBCreatesWithoutWaitingForFirstShare(t *testing.T) {
	preserveManagedSMBFuncs(t)
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "context-marker")
	ensureManagedSMBBackendFunc = func(got context.Context) error { require.Equal(t, ctx, got); return nil }
	loadManagedSMBClusterFunc = func(got context.Context, _ string) (map[string]any, error) {
		require.Equal(t, ctx, got)
		return nil, os.ErrNotExist
	}
	created := false
	createManagedSMBClusterFunc = func(got context.Context, request types.ManagedSMBService, targets []string) error {
		require.Equal(t, ctx, got)
		require.Equal(t, []string{"node-a"}, targets)
		created = true
		return nil
	}
	// No database membership read or polling: Ceph will not place until a share exists.
	err := EnableManagedSMB(ctx, managedSMBTestState(t, "node-a"), types.ManagedSMBService{ClusterID: "files", UserGroupRefs: []string{"users"}})
	require.NoError(t, err)
	require.True(t, created)
}

func TestEnableManagedSMBReadsDesiredPlacementNotObservedMembers(t *testing.T) {
	preserveManagedSMBFuncs(t)
	ensureManagedSMBBackendFunc = func(context.Context) error { return nil }
	loadManagedSMBClusterFunc = func(context.Context, string) (map[string]any, error) {
		return managedResource("node-a", "node-b"), nil
	}
	var applied map[string]any
	applyManagedSMBClusterFunc = func(_ context.Context, resource map[string]any) error { applied = resource; return nil }
	err := EnableManagedSMB(context.Background(), managedSMBTestState(t, "node-c"), types.ManagedSMBService{
		ClusterID: "files", BindAddresses: []string{"192.0.2.10", "192.0.2.11"}, Port: 1445,
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"hosts": []string{"node-a", "node-b", "node-c"}, "count": 3}, applied["placement"])
	assert.Equal(t, []map[string]string{{"address": "192.0.2.10"}, {"address": "192.0.2.11"}}, applied["bind_addrs"])
	assert.Equal(t, map[string]any{"smb": 1445}, applied["custom_ports"])
}

func TestEnableManagedSMBLookupFailureDoesNotCreate(t *testing.T) {
	preserveManagedSMBFuncs(t)
	ensureManagedSMBBackendFunc = func(context.Context) error { return nil }
	loadManagedSMBClusterFunc = func(context.Context, string) (map[string]any, error) { return nil, assert.AnError }
	createManagedSMBClusterFunc = func(context.Context, types.ManagedSMBService, []string) error {
		t.Fatal("lookup failure must not be treated as absence")
		return nil
	}
	err := EnableManagedSMB(context.Background(), managedSMBTestState(t, "node-a"), types.ManagedSMBService{ClusterID: "files", UserGroupRefs: []string{"users"}})
	require.ErrorIs(t, err, assert.AnError)
}

func TestEnableManagedSMBRequiresCreationCredentials(t *testing.T) {
	preserveManagedSMBFuncs(t)
	ensureManagedSMBBackendFunc = func(context.Context) error { return nil }
	loadManagedSMBClusterFunc = func(context.Context, string) (map[string]any, error) { return nil, os.ErrNotExist }
	err := EnableManagedSMB(context.Background(), managedSMBTestState(t, "node-a"), types.ManagedSMBService{ClusterID: "files"})
	require.ErrorContains(t, err, "requires a user")
}

func TestEnableManagedSMBReappliesExistingMemberForRecovery(t *testing.T) {
	preserveManagedSMBFuncs(t)
	ensureManagedSMBBackendFunc = func(context.Context) error { return nil }
	loadManagedSMBClusterFunc = func(context.Context, string) (map[string]any, error) { return managedResource("node-a"), nil }
	calls := 0
	applyManagedSMBClusterFunc = func(context.Context, map[string]any) error { calls++; return nil }
	require.NoError(t, EnableManagedSMB(context.Background(), managedSMBTestState(t, "node-a"), types.ManagedSMBService{ClusterID: "files"}))
	require.Equal(t, 1, calls, "desired membership alone is not evidence of successful local placement")
}

func TestDisableManagedSMBRemovesDesiredButUnobservedMember(t *testing.T) {
	preserveManagedSMBFuncs(t)
	loadManagedSMBClusterFunc = func(context.Context, string) (map[string]any, error) { return managedResource("node-a", "node-b"), nil }
	var applied map[string]any
	applyManagedSMBClusterFunc = func(_ context.Context, resource map[string]any) error { applied = resource; return nil }
	require.NoError(t, DisableManagedSMB(context.Background(), managedSMBTestState(t, "node-b"), "files"))
	require.Equal(t, map[string]any{"hosts": []string{"node-a"}, "count": 1}, applied["placement"])
}

func TestDisableManagedSMBCleansUnobservedLocalTargetAfterApply(t *testing.T) {
	preserveManagedSMBFuncs(t)
	loadManagedSMBClusterFunc = func(context.Context, string) (map[string]any, error) { return managedResource("node-a"), nil }
	events := []string{}
	applyManagedSMBClusterFunc = func(context.Context, map[string]any) error { events = append(events, "apply"); return nil }
	disableSMBLocalFunc = func(context.Context, interfaces.StateInterface, string) error {
		events = append(events, "cleanup")
		return nil
	}
	require.NoError(t, DisableManagedSMB(context.Background(), managedSMBTestState(t, "node-b"), "files"))
	require.Equal(t, []string{"apply", "cleanup"}, events)
}

func TestDisableManagedSMBRemovesFinalMember(t *testing.T) {
	preserveManagedSMBFuncs(t)
	loadManagedSMBClusterFunc = func(context.Context, string) (map[string]any, error) { return managedResource("node-a"), nil }
	removed := false
	removeManagedSMBClusterFunc = func(_ context.Context, id string) error { require.Equal(t, "files", id); removed = true; return nil }
	require.NoError(t, DisableManagedSMB(context.Background(), managedSMBTestState(t, "node-a"), "files"))
	require.True(t, removed)
}

func TestEnsureManagedSMBBackendUsesContext(t *testing.T) {
	runner := mocks.NewRunner(t)
	ctx := context.Background()
	for _, module := range []string{"microceph", "smb"} {
		runner.On("RunCommandContext", ctx, "ceph", "mgr", "module", "enable", module).Return("", nil).Once()
	}
	runner.On("RunCommandContext", ctx, "ceph", "orch", "set", "backend", "microceph").Return("", nil).Once()
	original := common.ProcessExec
	t.Cleanup(func() { common.ProcessExec = original })
	common.ProcessExec = runner
	require.NoError(t, ensureManagedSMBBackend(ctx))
}

func TestLoadManagedSMBClusterDistinguishesAbsenceAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		absent, fail bool
	}{
		{"absent", `{"resources":[]}`, true, true},
		{"unavailable", `{"error":"database unavailable","resources":[]}`, false, true},
		{"malformed", `{}`, false, true},
		{"present", `{"resource_type":"ceph.smb.cluster","cluster_id":"files"}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := mocks.NewRunner(t)
			runner.On("RunCommandContext", mock.Anything, "ceph", "smb", "show", "ceph.smb.cluster.files", "--format", "json").Return(tc.output, nil).Once()
			original := common.ProcessExec
			t.Cleanup(func() { common.ProcessExec = original })
			common.ProcessExec = runner
			_, err := loadManagedSMBCluster(context.Background(), "files")
			require.Equal(t, tc.fail, err != nil)
			require.Equal(t, tc.absent, errors.Is(err, os.ErrNotExist))
		})
	}
}

func TestCreateManagedSMBCredentialsStayInProtectedResourceFile(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "error"}[fail], func(t *testing.T) {
			runner := mocks.NewRunner(t)
			original := common.ProcessExec
			t.Cleanup(func() { common.ProcessExec = original })
			common.ProcessExec = runner
			path := ""
			var runErr error
			if fail {
				runErr = errors.New("raw stderr includes secret-password")
			}
			runner.On("RunCommandContext", mock.Anything, "ceph", "smb", "apply", "-i", mock.Anything, "--format", "json", "--password-filter-out", "hidden").Run(func(args mock.Arguments) {
				path = args.String(5)
				info, err := os.Stat(path)
				require.NoError(t, err)
				require.Equal(t, os.FileMode(0600), info.Mode().Perm())
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				var resources []map[string]any
				require.NoError(t, json.Unmarshal(data, &resources))
				require.Len(t, resources, 2)
				require.Equal(t, "ceph.smb.usersgroups", resources[0]["resource_type"])
				require.Equal(t, "files", resources[0]["linked_to_cluster"])
				require.Equal(t, "always", resources[1]["clustering"])
				for _, arg := range args[1:] {
					require.NotContains(t, arg, "secret-password")
				}
			}).Return(`{"success":true,"results":[{"success":true},{"success":true}]}`, runErr).Once()
			err := createManagedSMBCluster(context.Background(), types.ManagedSMBService{
				ClusterID: "files", Credentials: &types.SMBCredentials{Users: []types.SMBUser{{Name: "alice", Password: "secret-password"}}},
			}, []string{"node-a"})
			if fail {
				require.ErrorIs(t, err, ErrManagedSMBOutcomeUnknown)
				require.NotContains(t, err.Error(), "secret-password")
			} else {
				require.NoError(t, err)
			}
			require.NoFileExists(t, path)
		})
	}
}

func TestManagedSMBMutationRequiresPositiveResourceResult(t *testing.T) {
	for _, output := range []string{`{"success":false,"resource":{"password":"secret-password"}}`, `{}`, `not-json`, `{"success":true,"results":[{"success":false}]}`} {
		runner := mocks.NewRunner(t)
		runner.On("RunCommandContext", mock.Anything, "ceph", "smb", "apply").Return(output, nil).Once()
		original := common.ProcessExec
		common.ProcessExec = runner
		err := runManagedSMBMutation(context.Background(), "smb", "apply")
		common.ProcessExec = original
		require.ErrorIs(t, err, ErrManagedSMBOutcomeUnknown)
		require.NotContains(t, err.Error(), "secret-password")
	}
}

func TestManagedSMBClusteringModeIsImmutable(t *testing.T) {
	for _, mode := range []string{"always", "never"} {
		for _, requested := range []string{"", "always", "never"} {
			t.Run(mode+"/"+requested, func(t *testing.T) {
				preserveManagedSMBFuncs(t)
				ensureManagedSMBBackendFunc = func(context.Context) error { return nil }
				loadManagedSMBClusterFunc = func(context.Context, string) (map[string]any, error) {
					resource := managedResource("node-a")
					resource["clustering"] = mode
					return resource, nil
				}
				applied := false
				applyManagedSMBClusterFunc = func(_ context.Context, resource map[string]any) error {
					applied = true
					require.Equal(t, mode, resource["clustering"])
					return nil
				}
				request := types.ManagedSMBService{ClusterID: "files", Port: 1445}
				if requested != "" {
					request.Clustering = &requested
				}
				err := EnableManagedSMB(context.Background(), managedSMBTestState(t, "node-a"), request)
				if requested != "" && requested != mode {
					require.ErrorContains(t, err, "cannot be changed")
					require.False(t, applied)
				} else {
					require.NoError(t, err)
					require.True(t, applied)
				}
			})
		}
	}
}

func TestManagedSMBNeverRejectsAdditionalMember(t *testing.T) {
	preserveManagedSMBFuncs(t)
	ensureManagedSMBBackendFunc = func(context.Context) error { return nil }
	loadManagedSMBClusterFunc = func(context.Context, string) (map[string]any, error) {
		resource := managedResource("node-a")
		resource["clustering"] = "never"
		return resource, nil
	}
	applyManagedSMBClusterFunc = func(context.Context, map[string]any) error { t.Fatal("must not mutate"); return nil }
	err := EnableManagedSMB(context.Background(), managedSMBTestState(t, "node-b"), types.ManagedSMBService{ClusterID: "files"})
	require.ErrorContains(t, err, "cannot accept additional members")
}

func TestManagedSMBPlacementRejectsSelectorsItCannotPreserve(t *testing.T) {
	for _, placement := range []map[string]any{
		{"count": float64(2)},
		{"hosts": []any{"node-a", "node-b"}, "count": float64(1)},
		{"hosts": []any{"node-a"}, "label": "smb"},
	} {
		_, err := managedSMBMembersFromResource(map[string]any{"placement": placement})
		require.Error(t, err)
	}
}
