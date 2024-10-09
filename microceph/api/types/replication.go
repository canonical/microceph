package types

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/constants"
)

// ################################## Generic Replication Request ##################################
type ReplicationRequestType string

// This value is split till '-' to get the API request value.
const (
	EnableReplicationRequest    ReplicationRequestType = "PUT-" + constants.EventEnableReplication
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

// ################################## RBD Replication Request ##################################
// RbdReplicationDirection defines Rbd mirror direction
type RbdReplicationDirection string

const (
	RbdReplicationDirectionRXOnly RbdReplicationDirection = "rx-only"
	RbdReplicationDirectionRXTX   RbdReplicationDirection = "rx-tx"
)

// RbdResourceType defines request resource type
type RbdResourceType string

const (
	RbdResourceDisabled RbdResourceType = "disabled"
	RbdResourcePool     RbdResourceType = "pool"
	RbdResourceImage    RbdResourceType = "image"
)

// RbdReplicationType defines mode of rbd mirroring
type RbdReplicationType string

const (
	RbdReplicationDisabled   RbdReplicationType = "disable"
	RbdReplicationJournaling RbdReplicationType = "journal"
	RbdReplicationSnapshot   RbdReplicationType = "snapshot"
)

// RbdReplicationRequest implements ReplicationRequest for RBD replication.
type RbdReplicationRequest struct {
	SourcePool  string `json:"source_pool" yaml:"source_pool"`
	SourceImage string `json:"source_image" yaml:"source_image"`
	RemoteName  string `json:"remote" yaml:"remote"`
	// snapshot in d,h,m format
	Schedule        string                 `json:"schedule" yaml:"schedule"`
	ReplicationType RbdReplicationType     `json:"replication_type" yaml:"replication_type"`
	ResourceType    RbdResourceType        `json:"resource_type" yaml:"resource_type"`
	RequestType     ReplicationRequestType `json:"request_type" yaml:"request_type"`
	IsForceOp       bool                   `json:"force" yaml:"force"`
	SkipAutoEnable  bool                   `json:"skipAutoEnable" yaml:"skipAutoEnable"`
}

// GetWorkloadType provides the workload name for replication request
func (req RbdReplicationRequest) GetWorkloadType() CephWorkloadType {
	return RbdWorkload
}

// GetAPIObjectId provides the API object id i.e. /replication/rbd/<object-id>
func (req RbdReplicationRequest) GetAPIObjectId() string {
	// If both Pool and Image values are present encode for query.
	if len(req.SourceImage) != 0 && len(req.SourcePool) != 0 {
		resource := url.QueryEscape(fmt.Sprintf("%s/%s", req.SourcePool, req.SourceImage))
		logger.Debugf("REPAPI: Resource: %s", resource)
		return resource
	}

	return req.SourcePool
}

// GetAPIRequestType provides the REST method for the request
func (req RbdReplicationRequest) GetAPIRequestType() string {
	frags := strings.Split(string(req.RequestType), "-")
	logger.Debugf("REPAPI: API frags: %v", frags)
	if len(frags) == 0 {
		return ""
	}

	return frags[0]
}

// GetWorkloadRequestType provides the event used as the FSM trigger.
func (req RbdReplicationRequest) GetWorkloadRequestType() string {
	frags := strings.Split(string(req.RequestType), "-")
	logger.Debugf("REPAPI: Workload frags: %v", frags)
	if len(frags) < 2 {
		return ""
	}

	return frags[1]
}
