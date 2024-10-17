package types

import (
	"github.com/canonical/microceph/microceph/constants"
)

// ################################## Generic Replication Request ##################################
// ReplicationRequestType defines the various events replication request types.
type ReplicationRequestType string

// This value is split till '-' to get the API request type and the event name encoded in one string.
const (
	EnableReplicationRequest    ReplicationRequestType = "POST-" + constants.EventEnableReplication
	ConfigureReplicationRequest ReplicationRequestType = "PUT-" + constants.EventConfigureReplication
	DisableReplicationRequest   ReplicationRequestType = "DELETE-" + constants.EventDisableReplication
	StatusReplicationRequest    ReplicationRequestType = "GET-" + constants.EventStatusReplication
	ListReplicationRequest      ReplicationRequestType = "GET-" + constants.EventListReplication
)

type CephWorkloadType string

const (
	RbdWorkload CephWorkloadType = "rbd"
	FsWorkload  CephWorkloadType = "cephfs"
	RgwWorkload CephWorkloadType = "rgw"
)

// ReplicationRequest is interface for all Replication implementations (rbd, cephfs, rgw).
// It defines methods used by:
// 1. client code to make the API request
// 2. Replication state machine to feed the correct event trigger.
type ReplicationRequest interface {
	GetWorkloadType() CephWorkloadType
	GetAPIObjectId() string
	GetAPIRequestType() string
	GetWorkloadRequestType() string
}
