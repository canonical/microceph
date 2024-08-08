package types

import (
	"fmt"
	"net/url"
)

// ################################## Generic Replication Request ##################################
type ReplicationRequestType string

// This value is split till '-' to get the API request value.
const (
	CreateReplicationRequest ReplicationRequestType = "PUT"
	DeleteReplicationRequest ReplicationRequestType = "DELETE"
	StatusReplicationRequest ReplicationRequestType = "GET"
	ListReplicationRequest   ReplicationRequestType = "GET-ALL"
)

type CephWorkloadType string

const (
	RbdWorkload CephWorkloadType = "rbd"
	FsWorkload  CephWorkloadType = "cephfs"
	RgwWorkload CephWorkloadType = "rgw"
)

type ReplicationRequest interface {
	GetCephWorkloadType() CephWorkloadType
	GetAPIObjectId() string
	GetRequestType() ReplicationRequestType
}

// ################################## RBD Replication Request ##################################
type RbdResourceType string

const (
	RbdResourcePool  RbdResourceType = "pool"
	RbdResourceImage RbdResourceType = "image"
)

type RbdReplicationType string

const (
	RbdReplicationJournaling RbdReplicationType = "journal"
	RbdReplicationSnapshot   RbdReplicationType = "snapshot"
)

type RbdReplicationRequest struct {
	SourcePool  string `json:"source_pool" yaml:"source_pool"`
	SourceImage string `json:"source_image" yaml:"source_image"`
	// snapshot in d,h,m format
	Schedule        string                 `json:"schedule" yaml:"schedule"`
	ReplicationType RbdReplicationType     `json:"replication_type" yaml:"replication_type"`
	ResourceType    RbdResourceType        `json:"resource_type" yaml:"resource_type"`
	RequestType     ReplicationRequestType `json:"request_type" yaml:"request_type"`
}

func (req RbdReplicationRequest) GetCephWorkloadType() CephWorkloadType {
	return RbdWorkload
}

func (req RbdReplicationRequest) GetAPIObjectId() string {
	// If both Pool and Image values are present encode for query.
	if len(req.SourceImage) != 0 && len(req.SourcePool) != 0 {
		return url.QueryEscape(fmt.Sprintf("%s/%s", req.SourcePool, req.SourceImage))
	}

	return req.SourcePool
}

func (req RbdReplicationRequest) GetRequestType() ReplicationRequestType {
	return req.RequestType
}
