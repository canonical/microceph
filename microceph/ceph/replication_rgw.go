package ceph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// RgwReplicationHandler implements ReplicationHandlerInterface for RGW
// multisite replication.
//
// Every fact it reports about replication is derived live from RADOS, and
// none of it is persisted: a secondary cluster keeps no replication state of
// its own, so a stored answer would be empty exactly where an operator most
// needs one.
//
// The database is read for one thing only, which remotes are imported, since
// that is how a peer's own sync logs are reached.
type RgwReplicationHandler struct {
	// Prefill objects: always populated before any handler is called.
	Realm     RgwRealm
	ZoneGroup RgwZoneGroup
	Zone      RgwZone
	// Request Info
	Request types.RgwReplicationRequest

	// Only populated during status requests.
	// The realm period, read only when the local zonegroup is not the
	// realm's master zonegroup: it is the one read that can name the
	// metadata master across zonegroups (see masterZoneName).
	Period RgwPeriod
	// Metadata sync markers, left zero valued on the metadata master.
	MetadataSync RgwMetadataSyncStatus
	// Data sync markers for each source zone, keyed by source zone name.
	DataSync map[string]RgwDataSyncStatus
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

	// Sync markers are only needed to answer a status request (the CephFS
	// precedent), and reading them costs one radosgw-admin call per source.
	if req.RequestType == types.StatusReplicationRequest {
		// The metadata master's name can only be resolved through the
		// realm period when it lives outside the local zonegroup.
		if !rh.ZoneGroup.IsMaster {
			rh.Period, err = GetRgwPeriod("", "")
			if err != nil {
				return err
			}
		}

		err = rh.preFillSyncStatus()
		if err != nil {
			return err
		}
	}

	return nil
}

// preFillSyncStatus reads this zone's own sync markers: its metadata progress,
// and its data progress against every other zone in the zonegroup. All of it
// is local progress with no peer contact, so it is safe to read before the
// state machine has decided anything.
func (rh *RgwReplicationHandler) preFillSyncStatus() error {
	var err error

	// The metadata master syncs from no one and reports an empty "init"
	// status. Reading it would only invite that emptiness to be mistaken for
	// a stalled secondary.
	if !rh.isMasterZone() {
		rh.MetadataSync, err = GetRgwMetadataSyncStatus("", "")
		if err != nil {
			return err
		}
	}

	rh.DataSync = map[string]RgwDataSyncStatus{}
	for _, zone := range rh.peerZones() {
		status, err := GetRgwDataSyncStatus(zone.Name, "", "")
		if err != nil {
			return err
		}

		rh.DataSync[zone.Name] = status
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

// StatusHandler reports the local zone's place in the multisite topology and
// how far it has got syncing from each of its peers.
func (rh *RgwReplicationHandler) StatusHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPRGW: Status handler, Req %v", rh.Request)

	if rh.Request.ResourceType == types.RgwResourceBucket {
		return fmt.Errorf("bucket scoped %s is not implemented for rgw", types.StatusReplicationRequest)
	}

	st := args[repArgState].(interfaces.CephState)
	remotes, err := getRgwRemotesByZone(ctx, st)
	if err != nil {
		return err
	}

	response := types.RgwReplicationResponseStatus{
		Realm:         rh.Realm.Name,
		RealmEpoch:    rh.Realm.Epoch,
		CurrentPeriod: rh.Realm.CurrentPeriod,
		ZoneGroup:     rh.ZoneGroup.Name,
		Zone:          rh.Zone.Name,
		IsMasterZone:  rh.isMasterZone(),
		MasterZone:    rh.masterZoneName(),
		Zones:         rh.zoneBriefs(),
		MetadataSync:  rh.metadataSyncBrief(remotes),
		DataSync:      rh.dataSyncBriefs(remotes),
	}

	// Marshal to json string
	data, err := json.Marshal(response)
	if err != nil {
		err := fmt.Errorf("failed to marshal resource status: %w", err)
		logger.Error(err.Error())
		return err
	}

	// pass response for API
	*args[repArgResponse].(*string) = string(data)
	return nil
}

// metadataSyncBrief compares this zone's metadata markers against the master's
// metadata log. The master itself syncs from no one, so it reports that
// instead of a comparison it cannot make.
func (rh *RgwReplicationHandler) metadataSyncBrief(remotes map[string]types.RemoteRecord) types.RgwReplicationSyncBrief {
	masterZone := rh.masterZoneName()
	if rh.isMasterZone() {
		return types.RgwReplicationSyncBrief{
			SourceZone:   masterZone,
			State:        types.RgwSyncStateMaster,
			BehindShards: []int{},
		}
	}

	remote, ok := remotes[masterZone]
	if !ok {
		// Without a remote for the master cluster its metadata log cannot
		// be read at all, which is not the same as being caught up.
		logger.Warnf("REPRGW: no remote is imported for master zone %q, its metadata log cannot be read", masterZone)
		return summariseRgwSyncVerdict(masterZone, "", rh.MetadataSync.Info, RgwSyncVerdict{PeerLogUnavailable: true})
	}

	masterLog, err := GetRgwMdlogStatus(remote.Name, remote.LocalName)
	if err != nil {
		logger.Warnf("REPRGW: failed to read the metadata log of remote %s: %v", remote.Name, err)
		masterLog = nil
	}

	verdict := ComputeRgwMetadataSyncVerdict(rh.MetadataSync, masterLog, rh.Realm.CurrentPeriod)
	return summariseRgwSyncVerdict(masterZone, remote.Name, rh.MetadataSync.Info, verdict)
}

// dataSyncBriefs compares this zone's data markers against each source zone's
// data log, one brief per peer zone in the zonegroup.
func (rh *RgwReplicationHandler) dataSyncBriefs(remotes map[string]types.RemoteRecord) []types.RgwReplicationSyncBrief {
	peers := rh.peerZones()
	briefs := make([]types.RgwReplicationSyncBrief, 0, len(peers))

	for _, zone := range peers {
		local := rh.DataSync[zone.Name]

		remote, ok := remotes[zone.Name]
		if !ok {
			// The source's data log lives in the source's own cluster, so
			// without a remote for it there is nothing to compare against.
			// Reading the local log here instead would compare this zone
			// with itself and always report caught up.
			logger.Warnf("REPRGW: no remote is imported for source zone %q, its data log cannot be read", zone.Name)
			briefs = append(briefs, summariseRgwSyncVerdict(zone.Name, "", local.Info, RgwSyncVerdict{PeerLogUnavailable: true}))
			continue
		}

		sourceLog, err := GetRgwDatalogStatus(remote.Name, remote.LocalName)
		if err != nil {
			logger.Warnf("REPRGW: failed to read the data log of remote %s: %v", remote.Name, err)
			sourceLog = nil
		}

		verdict := ComputeRgwDataSyncVerdict(local, sourceLog)
		briefs = append(briefs, summariseRgwSyncVerdict(zone.Name, remote.Name, local.Info, verdict))
	}

	return briefs
}

// zoneBriefs describes every member of the local zonegroup.
func (rh *RgwReplicationHandler) zoneBriefs() []types.RgwReplicationZoneBrief {
	briefs := make([]types.RgwReplicationZoneBrief, 0, len(rh.ZoneGroup.Zones))
	for _, zone := range rh.ZoneGroup.Zones {
		briefs = append(briefs, types.RgwReplicationZoneBrief{
			Name:      zone.Name,
			ID:        zone.ID,
			Endpoints: zone.Endpoints,
			IsMaster:  zone.ID == rh.ZoneGroup.MasterZone,
			IsLocal:   zone.ID == rh.Zone.ID,
		})
	}

	return briefs
}

// isMasterZone reports whether the local zone is the realm's metadata
// master: the master zone of the realm's master zonegroup. Being master of
// a non-master zonegroup is not enough, since such a zone still syncs its
// metadata from the realm's master. Mirrors RGWSI_Zone::is_meta_master in
// Ceph's src/rgw/services/svc_zone.cc. The zonegroup names its master by
// id, never by name.
func (rh *RgwReplicationHandler) isMasterZone() bool {
	return rh.ZoneGroup.IsMaster && len(rh.Zone.ID) != 0 && rh.ZoneGroup.MasterZone == rh.Zone.ID
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

// masterZoneName resolves the realm's metadata master - the master zone of
// the realm's master zonegroup - to a zone name. When the local zonegroup
// is the master zonegroup the answer is one of its own members; otherwise
// the master lives in a zonegroup a plain zonegroup get can never see, and
// the realm period, which carries every zonegroup, answers instead.
func (rh *RgwReplicationHandler) masterZoneName() string {
	if rh.ZoneGroup.IsMaster {
		for _, zone := range rh.ZoneGroup.Zones {
			if zone.ID == rh.ZoneGroup.MasterZone {
				return zone.Name
			}
		}

		return ""
	}

	for _, zonegroup := range rh.Period.PeriodMap.ZoneGroups {
		if zonegroup.ID != rh.Period.MasterZonegroup {
			continue
		}

		for _, zone := range zonegroup.Zones {
			if zone.ID == rh.Period.MasterZone {
				return zone.Name
			}
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

// getRgwRemotesByZone indexes the imported remotes by the zone name each one
// reaches.
//
// A peer's sync logs live in the peer's own cluster, so reading them needs the
// conf and keyring that `remote import` renders. Multisite names a peer's zone
// after the remote record it was created from, which is what makes this lookup
// possible without storing anything: a remote named siteb reaches the cluster
// hosting the zone named siteb. A zone with no matching remote is reported as
// unreadable rather than guessed at.
func getRgwRemotesByZone(ctx context.Context, st interfaces.CephState) (map[string]types.RemoteRecord, error) {
	records, err := database.GetRemoteDb(ctx, st.ClusterState(), "")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch the imported remotes: %w", err)
	}

	remotes := make(map[string]types.RemoteRecord, len(records))
	for _, record := range records {
		remotes[record.Name] = record
	}

	return remotes, nil
}

// summariseRgwSyncVerdict renders one sync verdict for an operator.
//
// The three inconclusive outcomes stay distinct from each other and from being
// behind. A peer whose log could not be read and a period that could not be
// compared are not claims about how far behind this zone is, and neither is a
// claim that it is caught up. Shard counts are only carried when the
// comparison actually ran, because a verdict that short circuited leaves them
// at zero, which would otherwise read as fully synced.
func summariseRgwSyncVerdict(sourceZone string, remoteName string, info RgwSyncInfo, verdict RgwSyncVerdict) types.RgwReplicationSyncBrief {
	brief := types.RgwReplicationSyncBrief{
		SourceZone:   sourceZone,
		RemoteName:   remoteName,
		SyncStatus:   info.Status,
		ShardCount:   info.NumShards,
		BehindShards: []int{},
	}

	switch {
	case verdict.PeriodMismatch:
		brief.State = types.RgwSyncStatePeriodMismatch
		return brief
	case verdict.PeerLogUnavailable:
		brief.State = types.RgwSyncStatePeerUnavailable
		return brief
	case verdict.CaughtUp:
		brief.State = types.RgwSyncStateCaughtUp
	default:
		brief.State = types.RgwSyncStateBehind
	}

	if len(verdict.BehindShards) != 0 {
		brief.BehindShards = verdict.BehindShards
	}
	brief.FullSyncShards = verdict.FullSyncShards

	return brief
}
