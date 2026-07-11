package ceph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// ErrInvalidSMBSpec marks SMBSpec validation failures so the API layer can
// map them to HTTP 400 instead of 500.
var ErrInvalidSMBSpec = errors.New("invalid smb spec")

// Injectable seams for unit tests.
var (
	smbClusterMembersFunc = smbClusterMembers
	smbEnableNodeFunc     = smbEnableNode
	smbDisableNodeFunc    = smbDisableNode
	smbRegenerateNodeFunc = smbRegenerateNode
)

// ResolveSMBPlacement resolves the spec placement to a sorted set of
// cluster member names. Phase 1 honors hosts and count (count picks the
// first N sorted candidates, matching cephadm's count-of-hosts semantics);
// label, host_pattern and count_per_host are rejected.
func ResolveSMBPlacement(spec *types.SMBSpec, members []string) ([]string, error) {
	placement := spec.Placement

	if placement.Label != "" {
		return nil, fmt.Errorf("placement: 'label' is not supported (microceph has no host labels)")
	}
	if placement.CountPerHost != 0 {
		return nil, fmt.Errorf("placement: 'count_per_host' is not supported")
	}
	if len(placement.HostPattern) > 0 && string(placement.HostPattern) != "null" {
		return nil, fmt.Errorf("placement: 'host_pattern' is not supported")
	}

	var candidates []string
	if len(placement.Hosts) > 0 {
		memberSet := make(map[string]bool, len(members))
		for _, member := range members {
			memberSet[member] = true
		}

		seen := make(map[string]bool, len(placement.Hosts))
		for _, host := range placement.Hosts {
			if !memberSet[host] {
				return nil, fmt.Errorf("placement: host '%s' is not a cluster member", host)
			}
			if !seen[host] {
				seen[host] = true
				candidates = append(candidates, host)
			}
		}
	} else if placement.Count > 0 {
		candidates = append(candidates, members...)
	} else {
		return nil, fmt.Errorf("placement requires 'hosts' or 'count'")
	}

	sort.Strings(candidates)

	if placement.Count > 0 {
		if placement.Count > len(candidates) {
			return nil, fmt.Errorf("placement: count %d exceeds the %d available hosts", placement.Count, len(candidates))
		}
		candidates = candidates[:placement.Count]
	}

	return candidates, nil
}

// DiffSMBPlacement returns the node sets to enable and disable to converge
// from the current membership to the desired one.
func DiffSMBPlacement(desired, current []string) ([]string, []string) {
	desiredSet := make(map[string]bool, len(desired))
	for _, node := range desired {
		desiredSet[node] = true
	}
	currentSet := make(map[string]bool, len(current))
	for _, node := range current {
		currentSet[node] = true
	}

	var toEnable, toDisable []string
	for _, node := range desired {
		if !currentSet[node] {
			toEnable = append(toEnable, node)
		}
	}
	for _, node := range current {
		if !desiredSet[node] {
			toDisable = append(toDisable, node)
		}
	}

	sort.Strings(toEnable)
	sort.Strings(toDisable)
	return toEnable, toDisable
}

// ApplySMB validates an SMBSpec payload, computes the placement diff
// against the recorded membership and drives per-node enable/disable
// across the cluster. Fan-out is fail-fast: a partial apply is converged
// by re-applying (the flow is idempotent).
func ApplySMB(ctx context.Context, s interfaces.StateInterface, payload string) error {
	// Canonicalize so stored configs compare stably: AddNew compacts the
	// raw spec on write, so every comparison must use compacted bytes too.
	var buf bytes.Buffer
	err := json.Compact(&buf, []byte(payload))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSMBSpec, err)
	}
	canonical := buf.String()

	sp := &SMBServicePlacement{}
	err = sp.PopulateParams(s, canonical)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSMBSpec, err)
	}

	members, err := smbClusterMembersFunc(s)
	if err != nil {
		return fmt.Errorf("failed to list cluster members: %w", err)
	}

	desired, err := ResolveSMBPlacement(&sp.Spec, members)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSMBSpec, err)
	}

	current, err := database.GroupedServicesQuery.GetGroupMembers(ctx, s, "smb", sp.Spec.ClusterID)
	if err != nil {
		return fmt.Errorf("failed to fetch smb cluster membership: %w", err)
	}

	// Refresh the stored spec on re-apply so joining nodes render from the
	// latest config.
	configChanged := false
	if len(current) > 0 {
		existing, err := database.GroupedServicesQuery.GetGroupConfig(ctx, s, "smb", sp.Spec.ClusterID)
		if err != nil {
			return fmt.Errorf("failed to fetch smb cluster config: %w", err)
		}
		if existing != canonical {
			err = database.GroupedServicesQuery.UpdateGroupConfig(ctx, s, "smb", sp.Spec.ClusterID, canonical)
			if err != nil {
				return fmt.Errorf("failed to update smb cluster config: %w", err)
			}
			configChanged = true
		}
	}

	toEnable, toDisable := DiffSMBPlacement(desired, current)
	logger.Infof("smb apply %s: enable %v, disable %v", sp.Spec.ClusterID, toEnable, toDisable)

	for _, node := range toEnable {
		err = smbEnableNodeFunc(ctx, s, node, canonical)
		if err != nil {
			return fmt.Errorf("failed to enable smb cluster '%s' on node '%s': %w", sp.Spec.ClusterID, node, err)
		}
	}

	for _, node := range toDisable {
		err = smbDisableNodeFunc(ctx, s, node, sp.Spec.ClusterID)
		if err != nil {
			return fmt.Errorf("failed to disable smb cluster '%s' on node '%s': %w", sp.Spec.ClusterID, node, err)
		}
	}

	// Membership or spec changes invalidate every member's rendered
	// configs (the nodes file must be identical cluster-wide), so
	// regenerate all desired members, one node at a time.
	if len(toEnable) > 0 || len(toDisable) > 0 || configChanged {
		for _, node := range desired {
			err = smbRegenerateNodeFunc(ctx, s, node, sp.Spec.ClusterID)
			if err != nil {
				return fmt.Errorf("failed to regenerate smb cluster '%s' on node '%s': %w", sp.Spec.ClusterID, node, err)
			}
		}
	}

	return nil
}

// RemoveSMB drives removal of an smb cluster from all its member nodes.
// The RADOS objects referenced by the spec belong to mgr/smb and are left
// untouched.
func RemoveSMB(ctx context.Context, s interfaces.StateInterface, clusterID string) error {
	current, err := database.GroupedServicesQuery.GetGroupMembers(ctx, s, "smb", clusterID)
	if err != nil {
		return fmt.Errorf("failed to fetch smb cluster membership: %w", err)
	}

	if len(current) == 0 {
		return api.StatusErrorf(http.StatusNotFound, "no smb cluster '%s'", clusterID)
	}

	for _, node := range current {
		err = smbDisableNodeFunc(ctx, s, node, clusterID)
		if err != nil {
			return fmt.Errorf("failed to disable smb cluster '%s' on node '%s': %w", clusterID, node, err)
		}
	}

	return nil
}

// ListSMB reports every smb cluster with its stored spec and current
// placement.
func ListSMB(ctx context.Context, s interfaces.StateInterface) ([]types.SMBServiceStatus, error) {
	rows, err := database.GroupedServicesQuery.GetGroupedServices(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch grouped services: %w", err)
	}

	membersByCluster := map[string][]string{}
	for _, row := range rows {
		if row.Service != "smb" {
			continue
		}
		membersByCluster[row.GroupID] = append(membersByCluster[row.GroupID], row.Member)
	}

	statuses := make([]types.SMBServiceStatus, 0, len(membersByCluster))
	for clusterID, members := range membersByCluster {
		config, err := database.GroupedServicesQuery.GetGroupConfig(ctx, s, "smb", clusterID)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch config for smb cluster '%s': %w", clusterID, err)
		}

		sort.Strings(members)
		statuses = append(statuses, types.SMBServiceStatus{
			ClusterID: clusterID,
			Spec:      json.RawMessage(config),
			PlacedOn:  members,
		})
	}

	sort.Slice(statuses, func(i, j int) bool { return statuses[i].ClusterID < statuses[j].ClusterID })
	return statuses, nil
}

// DisableSMB tears this node out of an smb cluster and removes its
// records.
func DisableSMB(ctx context.Context, s interfaces.StateInterface, clusterID string) error {
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}
	return disableSMBLocal(ctx, s, clusterID, NewSMBRenderParams(clusterID, hostname, true))
}

// smbClusterMembers lists the cluster member names.
func smbClusterMembers(s interfaces.StateInterface) ([]string, error) {
	cli, err := s.ClusterState().Connect().Leader(false)
	if err != nil {
		return nil, err
	}
	return client.MClient.GetClusterMembers(cli)
}

// smbEnableNode runs the smb placement flow on the given node, locally or
// via the node-scoped endpoint.
func smbEnableNode(ctx context.Context, s interfaces.StateInterface, node, payload string) error {
	data := types.EnableService{Name: "smb", Wait: true, Payload: payload}
	if node == s.ClusterState().Name() {
		return ServicePlacementHandler(ctx, s, data)
	}

	cli, err := s.ClusterState().Connect().Leader(false)
	if err != nil {
		return err
	}
	return client.EnableSMBNodeService(ctx, cli, node, &data)
}

// smbRegenerateNode re-renders configs and restarts ctdbd on the given
// node, locally or via the node-scoped endpoint.
func smbRegenerateNode(ctx context.Context, s interfaces.StateInterface, node, clusterID string) error {
	if node == s.ClusterState().Name() {
		return RegenerateSMBNode(ctx, s, clusterID)
	}

	cli, err := s.ClusterState().Connect().Leader(false)
	if err != nil {
		return err
	}
	return client.RegenerateSMBNodeService(ctx, cli, node, &types.SMBService{ClusterID: clusterID})
}

// smbDisableNode tears down smb membership on the given node, locally or
// via the node-scoped endpoint.
func smbDisableNode(ctx context.Context, s interfaces.StateInterface, node, clusterID string) error {
	if node == s.ClusterState().Name() {
		return DisableSMB(ctx, s, clusterID)
	}

	cli, err := s.ClusterState().Connect().Leader(false)
	if err != nil {
		return err
	}
	return client.DeleteSMBNodeService(ctx, cli, node, &types.SMBService{ClusterID: clusterID})
}
