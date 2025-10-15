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
