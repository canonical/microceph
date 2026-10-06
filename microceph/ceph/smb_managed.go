package ceph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"

	"github.com/pborman/uuid"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

var ensureManagedSMBBackendFunc = ensureManagedSMBBackend
var createManagedSMBClusterFunc = createManagedSMBCluster
var loadManagedSMBClusterFunc = loadManagedSMBCluster
var applyManagedSMBClusterFunc = applyManagedSMBCluster
var removeManagedSMBClusterFunc = removeManagedSMBCluster
var disableSMBLocalFunc = DisableSMB
var getSMBServiceGroupConfigFunc = database.GetSMBServiceGroupConfig
var finalizeSMBServiceGroupIfUnchangedFunc = func(ctx context.Context, s interfaces.StateInterface, clusterID, config string) error {
	return database.FinalizeSMBServiceGroup(ctx, s, clusterID, config)
}

// ErrManagedSMBOutcomeUnknown means remote work may still be running or partially
// applied. Inspect upstream state rather than blindly retrying the mutation.
var ErrManagedSMBOutcomeUnknown = errors.New("SMB update outcome is uncertain; inspect Ceph state before retrying")

// EnableManagedSMB submits a target's configuration to Ceph's SMB manager.
// Concurrent full-placement updates follow upstream replacement semantics.
// Initial creation succeeds without member callbacks: Ceph waits for a share.
func EnableManagedSMB(ctx context.Context, s interfaces.StateInterface, request types.ManagedSMBService) error {
	err := ensureManagedSMBBackendFunc(ctx)
	if err != nil {
		return err
	}
	resource, err := loadManagedSMBClusterFunc(ctx, request.ClusterID)
	if errors.Is(err, os.ErrNotExist) {
		if request.Credentials == nil && len(request.UserGroupRefs) == 0 {
			return fmt.Errorf("new managed SMB cluster requires a user or user-group resource")
		}
		return createManagedSMBClusterFunc(ctx, request, []string{s.ClusterState().Name()})
	}
	if err != nil {
		return fmt.Errorf("failed to load managed SMB cluster: %w", err)
	}
	if request.Credentials != nil || len(request.UserGroupRefs) > 0 {
		return fmt.Errorf("SMB user configuration can only be supplied when creating the cluster")
	}
	members, err := managedSMBMembersFromResource(resource)
	if err != nil {
		return err
	}
	mode, ok := resource["clustering"].(string)
	if !ok || (mode != "always" && mode != "never") {
		return fmt.Errorf("managed SMB requires an explicit upstream clustering mode (always or never)")
	}
	if request.Clustering != nil && *request.Clustering != mode {
		return fmt.Errorf("SMB clustering mode %q cannot be changed", mode)
	}
	members = appendUniqueSMBMember(members, s.ClusterState().Name())
	if mode == "never" && len(members) > 1 {
		return fmt.Errorf("non-clustered SMB cannot accept additional members; recreate with --clustering always")
	}
	updateManagedSMBResource(resource, members, request)
	// Reapply even an existing desired member: a previous attempt may not have
	// placed it. Node-local effective-config checks avoid unnecessary restarts.
	return applyManagedSMBClusterFunc(ctx, resource)
}

// DisableManagedSMB removes a target from upstream desired placement, including
// a target which never reached the observed member database.
func DisableManagedSMB(ctx context.Context, s interfaces.StateInterface, clusterID string) error {
	resource, err := loadManagedSMBClusterFunc(ctx, clusterID)
	if errors.Is(err, os.ErrNotExist) {
		config, _, groupErr := getSMBServiceGroupConfigFunc(ctx, s, clusterID)
		if groupErr != nil {
			return groupErr
		}
		err = removeManagedSMBClusterFunc(ctx, clusterID)
		if err != nil {
			return err
		}
		err = disableSMBLocalFunc(ctx, s, clusterID)
		if err != nil {
			return err
		}
		return finalizeSMBServiceGroupIfUnchangedFunc(ctx, s, clusterID, config)
	}
	if err != nil {
		return fmt.Errorf("failed to load managed SMB cluster: %w", err)
	}
	members, err := managedSMBMembersFromResource(resource)
	if err != nil {
		return err
	}
	remaining, _ := removeManagedSMBMember(members, s.ClusterState().Name())
	finalize := len(remaining) == 0
	config := ""
	if finalize {
		config, _, err = getSMBServiceGroupConfigFunc(ctx, s, clusterID)
		if err != nil {
			return err
		}
		err = removeManagedSMBClusterFunc(ctx, clusterID)
	} else {
		updateManagedSMBResource(resource, remaining, types.ManagedSMBService{})
		err = applyManagedSMBClusterFunc(ctx, resource)
	}
	if err != nil {
		return err
	}
	// The backend removes observed members only. Also clean this target after
	// successful upstream removal in case its earlier placement was interrupted
	// before a grouped_services record was written. This path is idempotent.
	err = disableSMBLocalFunc(ctx, s, clusterID)
	if err != nil {
		return err
	}
	if finalize {
		return finalizeSMBServiceGroupIfUnchangedFunc(ctx, s, clusterID, config)
	}
	return nil
}

func ensureManagedSMBBackend(ctx context.Context) error {
	commands := [][]string{
		{"mgr", "module", "enable", "microceph"},
		{"orch", "set", "backend", "microceph"},
		{"mgr", "module", "enable", "smb"},
	}
	for _, args := range commands {
		_, err := cephRunContext(ctx, args...)
		if err != nil {
			return fmt.Errorf("failed preparing SMB manager: %w", ErrManagedSMBOutcomeUnknown)
		}
	}
	return nil
}

func createManagedSMBCluster(ctx context.Context, request types.ManagedSMBService, targets []string) error {
	err := request.Credentials.Validate()
	if err != nil {
		return err
	}
	if request.Credentials != nil && len(request.UserGroupRefs) > 0 {
		return fmt.Errorf("provide either credentials or user-group references, not both")
	}
	resources := []any{}
	refs := append([]string(nil), request.UserGroupRefs...)
	if request.Credentials != nil {
		// Tentacle resource IDs are at most 18 alphanumeric/hyphen characters.
		ref := strings.ReplaceAll(uuid.NewRandom().String(), "-", "")[:18]
		refs = append(refs, ref)
		resources = append(resources, map[string]any{
			"resource_type":     "ceph.smb.usersgroups",
			"users_groups_id":   ref,
			"linked_to_cluster": request.ClusterID,
			"values":            map[string]any{"users": request.Credentials.Users, "groups": []any{}},
		})
	}
	sources := make([]map[string]string, 0, len(refs))
	for _, ref := range refs {
		sources = append(sources, map[string]string{"source_type": "resource", "ref": ref})
	}
	mode := "always"
	if request.Clustering != nil {
		mode = *request.Clustering
	}
	if mode != "always" && mode != "never" {
		return fmt.Errorf("SMB clustering must be always or never")
	}
	if mode == "never" && len(targets) != 1 {
		return fmt.Errorf("non-clustered SMB requires exactly one member")
	}
	resource := map[string]any{
		"resource_type": "ceph.smb.cluster", "cluster_id": request.ClusterID,
		"auth_mode": "user", "clustering": mode, "user_group_settings": sources,
	}
	updateManagedSMBResource(resource, targets, request)
	resources = append(resources, resource)
	return applyManagedSMBResources(ctx, resources)
}

func loadManagedSMBCluster(ctx context.Context, clusterID string) (map[string]any, error) {
	name := fmt.Sprintf("ceph.smb.cluster.%s", clusterID)
	output, err := cephRunContext(ctx, "smb", "show", name, "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("failed querying SMB resource: %w", err)
	}
	resource := map[string]any{}
	err = json.Unmarshal([]byte(output), &resource)
	if err != nil {
		return nil, fmt.Errorf("invalid SMB cluster response")
	}
	if _, failed := resource["error"]; failed {
		return nil, fmt.Errorf("Ceph could not read the SMB cluster resource")
	}
	if resources, ok := resource["resources"].([]any); ok && len(resources) == 0 {
		return nil, os.ErrNotExist
	}
	if resource["resource_type"] != "ceph.smb.cluster" || resource["cluster_id"] != clusterID {
		return nil, fmt.Errorf("unexpected SMB cluster response")
	}
	return resource, nil
}

func applyManagedSMBCluster(ctx context.Context, resource map[string]any) error {
	return applyManagedSMBResources(ctx, resource)
}

func applyManagedSMBResources(ctx context.Context, resource any) error {
	data, err := json.Marshal(resource)
	if err != nil {
		return fmt.Errorf("failed to encode SMB cluster resource")
	}
	file, err := os.CreateTemp("", "microceph-managed-smb-*.json")
	if err != nil {
		return fmt.Errorf("failed to create SMB resource file: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)
	_, err = file.Write(data)
	if err != nil {
		file.Close()
		return fmt.Errorf("failed to write SMB resource file: %w", err)
	}
	err = file.Close()
	if err != nil {
		return fmt.Errorf("failed to close SMB resource file: %w", err)
	}
	return runManagedSMBMutation(ctx, "smb", "apply", "-i", path, "--format", "json", "--password-filter-out", "hidden")
}

func removeManagedSMBCluster(ctx context.Context, clusterID string) error {
	return runManagedSMBMutation(ctx, "smb", "cluster", "rm", clusterID, "--format", "json")
}

func runManagedSMBMutation(ctx context.Context, args ...string) error {
	output, err := cephRunContext(ctx, args...)
	if err != nil {
		// A RunError contains argv/stderr, which may include credentials. A
		// failed process also does not prove remote callbacks have stopped.
		return ErrManagedSMBOutcomeUnknown
	}
	var result struct {
		Success *bool `json:"success"`
		Results []struct {
			Success *bool `json:"success"`
		} `json:"results"`
	}
	err = json.Unmarshal([]byte(output), &result)
	if err != nil || result.Success == nil || !*result.Success {
		return ErrManagedSMBOutcomeUnknown
	}
	for _, item := range result.Results {
		if item.Success == nil || !*item.Success {
			return ErrManagedSMBOutcomeUnknown
		}
	}
	return nil
}

// managedSMBMembersFromResource refuses placement expressions that a host-list
// edit would silently reinterpret. Direct Ceph callers can manage those specs.
func managedSMBMembersFromResource(resource map[string]any) ([]string, error) {
	placement, ok := resource["placement"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("managed SMB requires explicit host placement")
	}
	for key := range placement {
		if key != "hosts" && key != "count" {
			return nil, fmt.Errorf("managed SMB cannot edit placement option %q; use ceph smb apply", key)
		}
	}
	hosts, ok := placement["hosts"].([]any)
	if !ok || len(hosts) == 0 {
		return nil, fmt.Errorf("managed SMB requires explicit host placement")
	}
	members := make([]string, 0, len(hosts))
	for _, entry := range hosts {
		host, ok := entry.(string)
		if !ok || host == "" || strings.ContainsAny(host, " :=\t\n\r") {
			return nil, fmt.Errorf("managed SMB requires plain member names in placement")
		}
		members = appendUniqueSMBMember(members, host)
	}
	if count, exists := placement["count"]; exists && count != float64(len(members)) {
		return nil, fmt.Errorf("managed SMB requires placement count to match explicit hosts")
	}
	return members, nil
}

func updateManagedSMBResource(resource map[string]any, members []string, request types.ManagedSMBService) {
	sort.Strings(members)
	resource["placement"] = map[string]any{"hosts": append([]string(nil), members...), "count": len(members)}
	bindType := "network"
	bindValues := request.BindNetworks
	if len(request.BindAddresses) > 0 {
		bindType = "address"
		bindValues = request.BindAddresses
	}
	if len(bindValues) > 0 {
		binds := make([]map[string]string, 0, len(bindValues))
		for _, value := range bindValues {
			if bindType == "network" {
				// Go accepts host bits in a CIDR; Ceph requires the canonical network.
				_, subnet, err := net.ParseCIDR(value)
				if err == nil {
					value = subnet.String()
				}
			}
			binds = append(binds, map[string]string{bindType: value})
		}
		resource["bind_addrs"] = binds
	}
	if request.Port != 0 {
		ports := map[string]any{}
		existing, ok := resource["custom_ports"].(map[string]any)
		if ok {
			for name, value := range existing {
				ports[name] = value
			}
		}
		ports["smb"] = request.Port
		resource["custom_ports"] = ports
	}
}

func appendUniqueSMBMember(members []string, target string) []string {
	for _, member := range members {
		if member == target {
			sort.Strings(members)
			return members
		}
	}
	members = append(members, target)
	sort.Strings(members)
	return members
}

func removeManagedSMBMember(members []string, target string) ([]string, bool) {
	remaining := make([]string, 0, len(members))
	found := false
	for _, member := range members {
		if member == target {
			found = true
			continue
		}
		remaining = append(remaining, member)
	}
	sort.Strings(remaining)
	return remaining, found
}
