package constants

// Replication Events
const (
	EventEnableReplication    = "enable_replication"
	EventDisableReplication   = "disable_replication"
	EventListReplication      = "list_replication"
	EventStatusReplication    = "status_replication"
	EventConfigureReplication = "configure_replication"
	EventPromoteReplication   = "promote_replication"
	EventDemoteReplication    = "demote_replication"
)

// RbdJournalingEnableFeatureSet is a slice of features needed for journaling replication in RBD.
var RbdJournalingEnableFeatureSet = [...]string{"exclusive-lock", "journaling"}

var (
	CephFSSubvolumePathPrefix   = "/volumes/"
	CephFSSubvolumePathTemplate = "/volumes/%s/%s"
	CephFSSubvolumeNoGroup      = "_nogroup"
)

// RGW multisite replication uses one realm and one zonegroup per deployment,
// both with fixed names: the operator never picks them, so nothing has to be
// carried between the two clusters.
const (
	// RgwRealmName is the realm MicroCeph creates for RGW replication.
	RgwRealmName = "microceph"
	// RgwZoneGroupName is the zonegroup MicroCeph creates for RGW replication.
	RgwZoneGroupName = "microceph"
	// RgwSiteResource is the sentinel resource an RGW replication request
	// carries in the API path for site scoped operations, i.e.
	// /1.0/ops/replication/rgw/site. Bucket scoped operations put the bucket
	// name there instead. The sentinel cannot be confused with a bucket that
	// happens to be named "site", because the handler dispatches on the
	// request body's resource type rather than on the path segment.
	RgwSiteResource = "site"
)
