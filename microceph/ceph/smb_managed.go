package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

var ensureManagedSMBBackendFunc = ensureManagedSMBBackend
var getManagedSMBMembersFunc = getManagedSMBMembers
var createManagedSMBClusterFunc = createManagedSMBCluster
var loadManagedSMBClusterFunc = loadManagedSMBCluster
var applyManagedSMBClusterFunc = applyManagedSMBCluster
var removeManagedSMBClusterFunc = removeManagedSMBCluster

// EnableManagedSMB adds the local target member to a managed SMB cluster.
func EnableManagedSMB(ctx context.Context, s interfaces.StateInterface, request types.ManagedSMBService) error {
	members, err := getManagedSMBMembersFunc(ctx, s, request.ClusterID)
	if err != nil {
		return fmt.Errorf("failed to list managed SMB members: %w", err)
	}

	target := s.ClusterState().Name()
	if len(members) == 0 && len(request.DefineUserPass) == 0 && len(request.UserGroupRefs) == 0 {
		resource, loadErr := loadManagedSMBClusterFunc(request.ClusterID)
		if loadErr == nil {
			members = appendUniqueSMBMember(managedSMBResourceMembers(resource), target)
			updateManagedSMBResource(resource, members, request)
			err = applyManagedSMBClusterFunc(resource)
			if err != nil {
				return fmt.Errorf("failed to update managed SMB cluster: %w", err)
			}
			return nil
		}
		return fmt.Errorf("new managed SMB cluster requires a user or user-group resource")
	}
	if len(members) == 0 {
		err = ensureManagedSMBBackendFunc(ctx)
		if err != nil {
			return fmt.Errorf("failed to prepare managed SMB backend: %w", err)
		}
		err = createManagedSMBClusterFunc(request, []string{target})
		if err != nil {
			return fmt.Errorf("failed to create managed SMB cluster: %w", err)
		}
		return nil
	}

	if len(request.DefineUserPass) > 0 || len(request.UserGroupRefs) > 0 {
		return fmt.Errorf("SMB user configuration can only be supplied when creating the cluster")
	}
	if containsManagedSMBMember(members, target) && len(request.BindAddresses) == 0 && len(request.BindNetworks) == 0 && request.Port == 0 {
		return nil
	}

	members = appendUniqueSMBMember(members, target)
	resource, err := loadManagedSMBClusterFunc(request.ClusterID)
	if err != nil {
		return fmt.Errorf("failed to load managed SMB cluster: %w", err)
	}
	updateManagedSMBResource(resource, members, request)
	err = applyManagedSMBClusterFunc(resource)
	if err != nil {
		return fmt.Errorf("failed to update managed SMB cluster: %w", err)
	}
	return nil
}

// DisableManagedSMB removes the local target member from a managed SMB cluster.
func DisableManagedSMB(ctx context.Context, s interfaces.StateInterface, clusterID string) error {
	members, err := getManagedSMBMembersFunc(ctx, s, clusterID)
	if err != nil {
		return fmt.Errorf("failed to list managed SMB members: %w", err)
	}

	target := s.ClusterState().Name()
	remaining, found := removeManagedSMBMember(members, target)
	if !found {
		return fmt.Errorf("SMB cluster %q is not enabled on target %q", clusterID, target)
	}
	if len(remaining) == 0 {
		err = removeManagedSMBClusterFunc(clusterID)
		if err != nil {
			return fmt.Errorf("failed to remove managed SMB cluster: %w", err)
		}
		return nil
	}

	resource, err := loadManagedSMBClusterFunc(clusterID)
	if err != nil {
		return fmt.Errorf("failed to load managed SMB cluster: %w", err)
	}
	updateManagedSMBResource(resource, remaining, types.ManagedSMBService{})
	err = applyManagedSMBClusterFunc(resource)
	if err != nil {
		return fmt.Errorf("failed to update managed SMB cluster: %w", err)
	}
	return nil
}

func ensureManagedSMBBackend(ctx context.Context) error {
	err := EnableMgrModule(ctx, "microceph", "", "")
	if err != nil {
		return err
	}
	_, err = cephRun("orch", "set", "backend", "microceph")
	if err != nil {
		return err
	}
	err = EnableMgrModule(ctx, "smb", "", "")
	if err != nil {
		return err
	}
	return nil
}

func getManagedSMBMembers(ctx context.Context, s interfaces.StateInterface, clusterID string) ([]string, error) {
	services, err := database.GroupedServicesQuery.GetGroupedServices(ctx, s)
	if err != nil {
		return nil, err
	}
	members := []string{}
	for _, service := range services {
		if service.Service == "smb" && service.GroupID == clusterID {
			members = append(members, service.Member)
		}
	}
	sort.Strings(members)
	return members, nil
}

func createManagedSMBCluster(request types.ManagedSMBService, targets []string) error {
	args := []string{
		"smb", "cluster", "create", request.ClusterID, "user",
		"--placement", managedSMBPlacement(targets),
	}
	for _, ref := range request.UserGroupRefs {
		args = append(args, "--user-group-ref", ref)
	}
	for _, userPass := range request.DefineUserPass {
		args = append(args, "--define-user-pass", userPass)
	}
	_, err := cephRun(args...)
	if err != nil {
		return err
	}

	if len(request.BindAddresses) == 0 && len(request.BindNetworks) == 0 && request.Port == 0 {
		return nil
	}
	resource, err := loadManagedSMBClusterFunc(request.ClusterID)
	if err != nil {
		return err
	}
	updateManagedSMBResource(resource, targets, request)
	return applyManagedSMBClusterFunc(resource)
}

func loadManagedSMBCluster(clusterID string) (map[string]any, error) {
	name := fmt.Sprintf("ceph.smb.cluster.%s", clusterID)
	output, err := cephRun("smb", "show", name, "--format", "json")
	if err != nil {
		return nil, err
	}
	resource := map[string]any{}
	err = json.Unmarshal([]byte(output), &resource)
	if err != nil {
		return nil, fmt.Errorf("failed to decode SMB cluster resource: %w", err)
	}
	return resource, nil
}

func applyManagedSMBCluster(resource map[string]any) error {
	data, err := json.Marshal(resource)
	if err != nil {
		return fmt.Errorf("failed to encode SMB cluster resource: %w", err)
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
	_, err = cephRun("smb", "apply", "-i", path)
	return err
}

func removeManagedSMBCluster(clusterID string) error {
	_, err := cephRun("smb", "cluster", "rm", clusterID)
	return err
}

func updateManagedSMBResource(resource map[string]any, members []string, request types.ManagedSMBService) {
	sort.Strings(members)
	resource["placement"] = map[string]any{
		"hosts": append([]string(nil), members...),
		"count": len(members),
	}
	if len(request.BindAddresses) > 0 {
		binds := make([]map[string]string, 0, len(request.BindAddresses))
		for _, address := range request.BindAddresses {
			binds = append(binds, map[string]string{"address": address})
		}
		resource["bind_addrs"] = binds
	} else if len(request.BindNetworks) > 0 {
		binds := make([]map[string]string, 0, len(request.BindNetworks))
		for _, network := range request.BindNetworks {
			binds = append(binds, map[string]string{"network": network})
		}
		resource["bind_addrs"] = binds
	}
	if request.Port != 0 {
		ports := map[string]int{}
		existing, ok := resource["custom_ports"].(map[string]any)
		if ok {
			for name, value := range existing {
				number, isNumber := value.(float64)
				if isNumber {
					ports[name] = int(number)
				}
			}
		}
		ports["smb"] = request.Port
		resource["custom_ports"] = ports
	}
}

func managedSMBResourceMembers(resource map[string]any) []string {
	placement, ok := resource["placement"].(map[string]any)
	if !ok {
		return nil
	}

	members := []string{}
	switch hosts := placement["hosts"].(type) {
	case []any:
		for _, host := range hosts {
			member, ok := host.(string)
			if ok && member != "" {
				members = append(members, member)
			}
		}
	case []string:
		members = append(members, hosts...)
	}
	sort.Strings(members)
	return members
}

func containsManagedSMBMember(members []string, target string) bool {
	for _, member := range members {
		if member == target {
			return true
		}
	}
	return false
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

func managedSMBPlacement(targets []string) string {
	sort.Strings(targets)
	return fmt.Sprintf("%d %s", len(targets), strings.Join(targets, " "))
}
