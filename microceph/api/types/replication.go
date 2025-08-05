package types

import (
	"strings"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/constants"
)

// ################################## Generic Replication Request ##################################
// ReplicationResourceType defines the resource type for any workload.
type ReplicationResourceType string

// ReplicationRequestType defines the various events replication request types.
type ReplicationRequestType string

// This value is split till '-' to get the API request type and the event name encoded in one string.
const (
	EnableReplicationRequest    ReplicationRequestType = "POST-" + constants.EventEnableReplication
	ConfigureReplicationRequest ReplicationRequestType = "PUT-" + constants.EventConfigureReplication
	PromoteReplicationRequest   ReplicationRequestType = "PUT-" + constants.EventPromoteReplication
	DemoteReplicationRequest    ReplicationRequestType = "PUT-" + constants.EventDemoteReplication
	// Delete Requests
	DisableReplicationRequest ReplicationRequestType = "DELETE-" + constants.EventDisableReplication
	// Get Requests
	StatusReplicationRequest ReplicationRequestType = "GET-" + constants.EventStatusReplication
	ListReplicationRequest   ReplicationRequestType = "GET-" + constants.EventListReplication
	// Workload request, used for site-wide workload operations like promote/demote.
	WorkloadReplicationRequest ReplicationRequestType = ""
)

type CephWorkloadType string

const (
	RbdWorkload CephWorkloadType = "rbd"
	CephFsWorkload  CephWorkloadType = "cephfs"
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

// GetAPIRequestTypeGeneric extracts and returns the REST API request type from a ReplicationRequestType.
func GetAPIRequestTypeGeneric(requestType ReplicationRequestType) string {
	frags := strings.Split(string(requestType), "-")
	logger.Debugf("REPAPI: Request frags: %v", frags)
	if len(frags) == 0 {
		return ""
	}

	return frags[0]
}

// GetWorkloadRequestTypeGeneric extracts and returns the replication request type from a ReplicationRequestType.
func GetWorkloadRequestTypeGeneric(requestType ReplicationRequestType) string {
	frags := strings.Split(string(requestType), "-")
	logger.Debugf("REPAPI: Request frags: %v", frags)
	if len(frags) < 2 {
		return ""
	}

	return frags[1]
}