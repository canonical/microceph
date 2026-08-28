package types

import (
	"net/url"

	"github.com/canonical/microceph/microceph/logger"
)

// ################################## RGW Replication Request ##################################

// RgwResourceType defines the scope of an RGW replication request.
type RgwResourceType ReplicationResourceType

const (
	// RgwResourceSite scopes a request to the whole cluster, i.e. to the
	// local zone's place in the multisite topology. It doubles as the
	// resource segment of the API path for such a request, i.e.
	// /1.0/ops/replication/rgw/site, because an empty segment would route
	// the request to the workload root endpoint instead, where a GET means
	// list rather than status.
	RgwResourceSite RgwResourceType = "site"
	// RgwResourceBucket scopes a request to a single bucket, whose name
	// fills the resource segment instead.
	RgwResourceBucket RgwResourceType = "bucket"
)

// RgwReplicationRequest implements ReplicationRequest for RGW replication.
//
// It carries only the fields the implemented verbs read. Enable, disable,
// configure and promote each need more - a remote name, replication modes,
// endpoint lists, a force flag - and each brings its own when it lands. The
// request body is decoded leniently, so a later field is an ordinary additive
// change rather than a break.
type RgwReplicationRequest struct {
	Bucket       string                 `json:"bucket" yaml:"bucket"`
	ResourceType RgwResourceType        `json:"resource_type" yaml:"resource_type"`
	RequestType  ReplicationRequestType `json:"request_type" yaml:"request_type"`
}

// GetWorkloadType provides the workload name for replication request
func (req RgwReplicationRequest) GetWorkloadType() CephWorkloadType {
	return RgwWorkload
}

// GetAPIObjectID provides the API object id i.e. /replication/rgw/<object-id>
//
// An empty id is what routes a request to the workload root endpoint, so it is
// returned for exactly the cluster wide verbs. Every other verb is site or
// bucket scoped and must carry a resource segment, or the daemon would answer
// it with the wrong event entirely.
func (req RgwReplicationRequest) GetAPIObjectID() string {
	switch req.RequestType {
	case WorkloadReplicationRequest, ListReplicationRequest, PromoteReplicationRequest, DemoteReplicationRequest:
		return ""
	}

	if len(req.Bucket) != 0 {
		resource := url.QueryEscape(req.Bucket)
		logger.Debugf("REPAPI: Resource: %s", resource)
		return resource
	}

	return string(RgwResourceSite)
}

// SetAPIObjectID populates the request from the API object id i.e.
// /replication/rgw/<object-id>
//
// The site sentinel carries no data. A bucket name does, but the request
// body's resource type stays authoritative, so a bucket named "site" is never
// mistaken for the sentinel.
func (req *RgwReplicationRequest) SetAPIObjectID(id string) error {
	// unescape object string
	object, err := url.PathUnescape(id)
	if err != nil {
		return err
	}

	if req.ResourceType == RgwResourceBucket {
		req.Bucket = object
	}

	return nil
}

// GetAPIRequestType provides the REST method for the request
func (req RgwReplicationRequest) GetAPIRequestType() string {
	return GetAPIRequestTypeGeneric(req.RequestType)
}

// GetWorkloadRequestType provides the event used as the FSM trigger.
func (req RgwReplicationRequest) GetWorkloadRequestType() string {
	return GetWorkloadRequestTypeGeneric(req.RequestType)
}

// OverwriteRequestType sets the RequestType param to provided value.
func (req *RgwReplicationRequest) OverwriteRequestType(overwriteRequestType ReplicationRequestType) {
	if len(overwriteRequestType) != 0 {
		req.RequestType = overwriteRequestType
	}
}

// ################################## RGW Replication Response ##################################

// RgwReplicationSyncState is one sync stream's verdict, rendered for an
// operator. The three inconclusive outcomes are deliberately distinct from
// each other and from being behind: a peer that could not be read is not a
// claim about how far behind this zone is.
type RgwReplicationSyncState string

const (
	// RgwSyncStateCaughtUp means every shard has been compared against the
	// peer's log head and none of them is behind.
	RgwSyncStateCaughtUp RgwReplicationSyncState = "caught-up"
	// RgwSyncStateBehind means at least one shard is behind, or is still
	// doing its first full copy.
	RgwSyncStateBehind RgwReplicationSyncState = "behind"
	// RgwSyncStatePeerUnavailable means the peer's log head could not be
	// read, so no comparison was possible.
	RgwSyncStatePeerUnavailable RgwReplicationSyncState = "peer-unavailable"
	// RgwSyncStatePeriodMismatch means this zone is on an older realm
	// period, so its markers cannot be compared with the peer's log.
	RgwSyncStatePeriodMismatch RgwReplicationSyncState = "period-mismatch"
	// RgwSyncStateMaster means the stream does not apply: this zone is the
	// metadata master and syncs from no one.
	RgwSyncStateMaster RgwReplicationSyncState = "master"
)

// RgwReplicationZoneBrief describes one member of the local zonegroup.
type RgwReplicationZoneBrief struct {
	Name      string   `json:"name" yaml:"name"`
	ID        string   `json:"id" yaml:"id"`
	Endpoints []string `json:"endpoints" yaml:"endpoints"`
	IsMaster  bool     `json:"is_master" yaml:"is_master"`
	IsLocal   bool     `json:"is_local" yaml:"is_local"`
}

// RgwReplicationSyncBrief reports how far the local zone has got syncing one
// stream: its metadata from the master, or its data from one source zone.
//
// SyncStatus is what radosgw-admin reports about the stream itself ("sync"
// once running, "init" before it starts), while State is the comparison
// against the peer's log. BehindShards and FullSyncShards are only meaningful
// when that comparison actually ran, i.e. when State is caught-up or behind.
//
// Note that a caught-up data stream means this zone has noticed every bucket
// the source logged activity for. It does not prove every object inside those
// buckets arrived, nor that no shard is retrying a failed object: reading that
// costs one radosgw-admin call per shard, which is too slow for a status
// command. Use `radosgw-admin bucket sync status` for per-object certainty.
type RgwReplicationSyncBrief struct {
	SourceZone     string                  `json:"source_zone" yaml:"source_zone"`
	RemoteName     string                  `json:"remote" yaml:"remote"`
	State          RgwReplicationSyncState `json:"state" yaml:"state"`
	SyncStatus     string                  `json:"sync_status" yaml:"sync_status"`
	ShardCount     int                     `json:"shard_count" yaml:"shard_count"`
	BehindShards   []int                   `json:"behind_shards" yaml:"behind_shards"`
	FullSyncShards int                     `json:"full_sync_shards" yaml:"full_sync_shards"`
}

// RgwReplicationResponseStatus is the site scoped status of RGW replication:
// the local zone's place in the multisite topology, plus one sync brief per
// stream flowing into it.
type RgwReplicationResponseStatus struct {
	Realm         string                    `json:"realm" yaml:"realm"`
	RealmEpoch    int                       `json:"realm_epoch" yaml:"realm_epoch"`
	CurrentPeriod string                    `json:"current_period" yaml:"current_period"`
	ZoneGroup     string                    `json:"zonegroup" yaml:"zonegroup"`
	Zone          string                    `json:"zone" yaml:"zone"`
	IsMasterZone  bool                      `json:"is_master_zone" yaml:"is_master_zone"`
	MasterZone    string                    `json:"master_zone" yaml:"master_zone"`
	Zones         []RgwReplicationZoneBrief `json:"zones" yaml:"zones"`
	MetadataSync  RgwReplicationSyncBrief   `json:"metadata_sync" yaml:"metadata_sync"`
	DataSync      []RgwReplicationSyncBrief `json:"data_sync" yaml:"data_sync"`
}
