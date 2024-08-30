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

// Slices
type MirrorPool struct {
	Name string
	Mode RbdResourceType
}

type MirrorImage struct {
	Name string
	Mode RbdReplicationType
}

type MirrorPools []MirrorPool
type MirrorImages []MirrorImage

// ################################## RBD Replication Request ##################################
type RbdResourceType string
type RbdReplicationDirection string

const (
	RbdReplicationDirectionRXOnly RbdReplicationDirection = "rx-only"
	RbdReplicationDirectionRXTX   RbdReplicationDirection = "rx-tx"
)

const (
	RbdResourceDisabled RbdResourceType = "disabled"
	RbdResourcePool     RbdResourceType = "pool"
	RbdResourceImage    RbdResourceType = "image"
)

type RbdReplicationType string

const (
	RbdReplicationDisabled   RbdReplicationType = "disable"
	RbdReplicationJournaling RbdReplicationType = "journal"
	RbdReplicationSnapshot   RbdReplicationType = "snapshot"
)

type RbdReplicationRequest struct {
	SourcePool  string `json:"source_pool" yaml:"source_pool"`
	SourceImage string `json:"source_image" yaml:"source_image"`
	RemoteName  string `json:"remote" yaml:"remote"`
	// snapshot in d,h,m format
	Schedule        string                 `json:"schedule" yaml:"schedule"`
	ReplicationType RbdReplicationType     `json:"replication_type" yaml:"replication_type"`
	ResourceType    RbdResourceType        `json:"resource_type" yaml:"resource_type"`
	RequestType     ReplicationRequestType `json:"request_type" yaml:"request_type"`
}

func (req RbdReplicationRequest) GetWorkloadType() CephWorkloadType {
	return RbdWorkload
}

func (req RbdReplicationRequest) GetAPIObjectId() string {
	// If both Pool and Image values are present encode for query.
	if len(req.SourceImage) != 0 && len(req.SourcePool) != 0 {
		resource := url.QueryEscape(fmt.Sprintf("%s/%s", req.SourcePool, req.SourceImage))
		logger.Infof("BAZINGA: Check the Resource: %s", resource)
		return resource
	}

	return req.SourcePool
}

func (req RbdReplicationRequest) GetAPIRequestType() string {
	frags := strings.Split(string(req.RequestType), "-")
	logger.Infof("BAZINGA: Check the API frags: %v", frags)
	if len(frags) == 0 {
		return ""
	}

	return frags[0]
}

func (req RbdReplicationRequest) GetWorkloadRequestType() string {
	frags := strings.Split(string(req.RequestType), "-")
	logger.Infof("BAZINGA: Check the workload frags: %v", frags)
	if len(frags) < 2 {
		return ""
	}

	return frags[1]
}
