package types

import (
	"github.com/canonical/microceph/microceph/constants"
)

// ################################## Generic Replication Request ##################################
type ReplicationRequestType string

// This value is split till '-' to get the API request value.
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

type ReplicationRequest interface {
	GetWorkloadType() CephWorkloadType
	GetAPIObjectId() string
	GetAPIRequestType() string
	GetWorkloadRequestType() string
}
