package ceph

import (
	"context"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/logger"
)

// RgwReplicationHandler implements ReplicationHandlerInterface for RGW
// multisite replication.
//
// Every fact it reports about replication is derived live from RADOS, and
// none of it is persisted: a secondary cluster keeps no replication state of
// its own, so a stored answer would be empty exactly where an operator most
// needs one.
type RgwReplicationHandler struct {
	// Prefill objects: always populated before any handler is called.
	Realm     RgwRealm
	ZoneGroup RgwZoneGroup
	Zone      RgwZone
	// Request Info
	Request types.RgwReplicationRequest
}

// PreFill populates the handler struct with the local multisite topology.
//
// The empty cluster and client pair on every read below targets this cluster;
// a non-empty pair would append --cluster and --id and answer about a peer
// instead.
//
// A cluster wide request arrives with no resource fields set at all, so
// nothing here may assume the request carries a zone, bucket or remote.
func (rh *RgwReplicationHandler) PreFill(ctx context.Context, request types.ReplicationRequest) error {
	var err error
	req := request.(types.RgwReplicationRequest)
	rh.Request = req

	// The read wrappers turn a failing radosgw-admin call into a zero value
	// with a nil error, so a gateway that is not configured for multisite
	// (or not running at all) reads as ordinary empty state here.
	rh.Realm, err = GetRgwRealm("", "")
	if err != nil {
		return err
	}

	// Without a realm there is no topology to describe, and every remaining
	// read would come back empty anyway.
	if len(rh.Realm.Name) == 0 {
		return nil
	}

	rh.ZoneGroup, err = GetRgwZoneGroup("", "")
	if err != nil {
		return err
	}

	rh.Zone, err = GetRgwZone("", "")
	if err != nil {
		return err
	}

	return nil
}

// GetResourceState fetches the replication state of the local RGW zone.
func (rh *RgwReplicationHandler) GetResourceState() (ReplicationState, error) {
	// No realm means a plain single site gateway, or no gateway at all.
	if len(rh.Realm.Name) == 0 {
		return StateDisabledReplication, nil
	}

	// A realm whose zonegroup no longer lists the local zone is a zone that
	// has been removed from the topology: nothing replicates to or from it.
	if !rh.isZoneGroupMember() {
		return StateDisabledReplication, nil
	}

	return StateEnabledReplication, nil
}

// EnableHandler is not implemented for the rgw workload yet.
func (rh *RgwReplicationHandler) EnableHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPRGW: Enable handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for rgw", types.EnableReplicationRequest)
}

// DisableHandler is not implemented for the rgw workload yet.
func (rh *RgwReplicationHandler) DisableHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPRGW: Disable handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for rgw", types.DisableReplicationRequest)
}

// ConfigureHandler is not implemented for the rgw workload yet.
func (rh *RgwReplicationHandler) ConfigureHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPRGW: Configure handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for rgw", types.ConfigureReplicationRequest)
}

// ListHandler is not implemented for the rgw workload yet.
func (rh *RgwReplicationHandler) ListHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPRGW: List handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for rgw", types.ListReplicationRequest)
}

// PromoteHandler is not implemented for the rgw workload yet.
func (rh *RgwReplicationHandler) PromoteHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPRGW: Promote handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for rgw", types.PromoteReplicationRequest)
}

// DemoteHandler is not implemented for the rgw workload yet.
func (rh *RgwReplicationHandler) DemoteHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPRGW: Demote handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for rgw", types.DemoteReplicationRequest)
}

// StatusHandler is not implemented for the rgw workload yet.
func (rh *RgwReplicationHandler) StatusHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPRGW: Status handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for rgw", types.StatusReplicationRequest)
}

// isMasterZone reports whether the local zone is the zonegroup's master. The
// zonegroup names its master by id, never by name.
func (rh *RgwReplicationHandler) isMasterZone() bool {
	return len(rh.Zone.ID) != 0 && rh.ZoneGroup.MasterZone == rh.Zone.ID
}

// isZoneGroupMember reports whether the local zone belongs to the zonegroup.
func (rh *RgwReplicationHandler) isZoneGroupMember() bool {
	if len(rh.Zone.ID) == 0 {
		return false
	}

	for _, zone := range rh.ZoneGroup.Zones {
		if zone.ID == rh.Zone.ID {
			return true
		}
	}

	return false
}

// masterZoneName resolves the zonegroup's master zone id to a zone name.
func (rh *RgwReplicationHandler) masterZoneName() string {
	for _, zone := range rh.ZoneGroup.Zones {
		if zone.ID == rh.ZoneGroup.MasterZone {
			return zone.Name
		}
	}

	return ""
}

// peerZones lists the zonegroup members other than the local zone.
func (rh *RgwReplicationHandler) peerZones() []RgwZoneGroupZone {
	peers := make([]RgwZoneGroupZone, 0, len(rh.ZoneGroup.Zones))
	for _, zone := range rh.ZoneGroup.Zones {
		if zone.ID == rh.Zone.ID {
			continue
		}

		peers = append(peers, zone)
	}

	return peers
}
