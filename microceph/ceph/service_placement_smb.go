package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

// smbPort is the well-known TCP port smbd binds on every placed node.
const smbPort = 445

// isAddressAvailableFunc is injectable so tests can exercise the
// hospitality check without binding the fixed SMB port.
var isAddressAvailableFunc = isAddressAvailable

// SMBServicePlacement implements PlacementIntf for CTDB-clustered Samba.
// The payload is the mgr/smb SMBSpec JSON, stored verbatim as the service
// group config.
type SMBServicePlacement struct {
	Spec    types.SMBSpec
	rawSpec string
}

// PopulateParams parses and validates the SMBSpec payload.
func (smb *SMBServicePlacement) PopulateParams(s interfaces.StateInterface, payload string) error {
	err := json.Unmarshal([]byte(payload), &smb.Spec)
	if err != nil {
		return err
	}

	err = checkSMBUnsupportedFields([]byte(payload))
	if err != nil {
		return err
	}

	if !types.SMBClusterIDRegex.MatchString(smb.Spec.ClusterID) {
		return fmt.Errorf("cluster_id '%s' is not a valid ID (regex: '%s')",
			smb.Spec.ClusterID, types.SMBClusterIDRegex.String())
	}

	for _, feature := range smb.Spec.Features {
		switch feature {
		case "clustered":
			// CTDB clustering is the Phase 1 deployment model.
		case "domain":
			return fmt.Errorf("features: 'domain' (AD membership) is not supported in Phase 1")
		default:
			return fmt.Errorf("features: '%s' is not supported", feature)
		}
	}

	smb.rawSpec = payload
	return nil
}

// checkSMBUnsupportedFields rejects SMBSpec fields Phase 1 does not
// implement, so a spec requesting them fails loudly instead of being
// silently ignored. Unset serializations (null, [], {}) are tolerated.
func checkSMBUnsupportedFields(payload []byte) error {
	var raw map[string]json.RawMessage
	err := json.Unmarshal(payload, &raw)
	if err != nil {
		return err
	}

	unsupported := func(key string) bool {
		switch key {
		case "bind_addrs", "custom_ports", "custom_dns":
			return true
		}
		return strings.HasPrefix(key, "remote_control_")
	}

	for key, value := range raw {
		if !unsupported(key) {
			continue
		}
		switch string(value) {
		case "null", "[]", "{}":
			continue
		}
		return fmt.Errorf("field '%s' is not supported in Phase 1", key)
	}

	return nil
}

// HospitalityCheck verifies the SMB port is free and the node is not
// already part of an smb cluster (smb clusters are node-disjoint: one
// ctdb/smbd instance per node).
func (smb *SMBServicePlacement) HospitalityCheck(s interfaces.StateInterface) error {
	address := fmt.Sprintf("0.0.0.0:%d", smbPort)
	available, err := isAddressAvailableFunc(address)
	if err != nil {
		return fmt.Errorf("error encountered during address availability check: %w", err)
	} else if !available {
		return fmt.Errorf("address '%s' is currently in use.", address)
	}

	services, err := database.GroupedServicesQuery.GetGroupedServicesOnHost(context.Background(), s)
	if err != nil {
		return fmt.Errorf("failed to fetch smb group membership: %w", err)
	}

	for _, service := range services {
		if service.Service != "smb" {
			continue
		}
		if service.GroupID == smb.Spec.ClusterID {
			return fmt.Errorf("node is already a member of smb cluster '%s'", service.GroupID)
		}
		return fmt.Errorf("node is already a member of smb cluster '%s'; smb clusters must be node-disjoint", service.GroupID)
	}

	return nil
}

// ServiceInit is a no-op in Phase 1 milestone M2: config rendering and
// ctdbd lifecycle land with M3.
func (smb *SMBServicePlacement) ServiceInit(ctx context.Context, s interfaces.StateInterface) error {
	return nil
}

// PostPlacementCheck is a no-op until ServiceInit starts services (M3),
// after which it will verify ctdbd health.
func (smb *SMBServicePlacement) PostPlacementCheck(s interfaces.StateInterface) error {
	return nil
}

// DbUpdate records the group membership, storing the SMBSpec JSON verbatim
// as the group config (single source of truth; no parallel schema).
func (smb *SMBServicePlacement) DbUpdate(ctx context.Context, s interfaces.StateInterface) error {
	return database.GroupedServicesQuery.AddNew(ctx, s, "smb", smb.Spec.ClusterID,
		json.RawMessage(smb.rawSpec), database.SMBServiceInfo{})
}
