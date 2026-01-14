package types

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/canonical/microceph/microceph/logger"
)

// Types for RBD Pool status table.

type RbdPoolStatusImageBrief struct {
	Name            string `json:"name" yaml:"name"`
	IsPrimary       bool   `json:"is_primary" yaml:"is_primary"`
	LastLocalUpdate string `json:"last_local_update" yaml:"last_local_update"`
}

type RbdPoolStatusRemoteBrief struct {
	Name      string `json:"name" yaml:"name"`
	UUID      string `json:"uuid" yaml:"uuid"`
	Direction string `json:"direction" yaml:"direction"`
}

type RbdPoolStatus struct {
	Name              string                     `json:"name" yaml:"name"`
	Type              string                     `json:"type" yaml:"type"`
	HealthReplication string                     `json:"rep_health" yaml:"rep_health"`
	HealthDaemon      string                     `json:"daemon_health" yaml:"daemon_health"`
	HealthImages      string                     `json:"image_health" yaml:"image_health"`
	ImageCount        int                        `json:"image_count" yaml:"image_count"`
	Images            []RbdPoolStatusImageBrief  `json:"images" yaml:"images"`
	Remotes           []RbdPoolStatusRemoteBrief `json:"remotes" yaml:"remotes"`
}

// Types for RBD Image status table.

type RbdImageStatusRemoteBrief struct {
	Name             string `json:"name" yaml:"name"`
	Status           string `json:"status" yaml:"status"`
	LastRemoteUpdate string `json:"last_remote_update" yaml:"last_remote_update"`
}

type RbdImageStatus struct {
	Name            string                      `json:"name" yaml:"name"`
	ID              string                      `json:"id" yaml:"id"`
	Type            string                      `json:"type" yaml:"type"`
	IsPrimary       bool                        `json:"is_primary" yaml:"is_primary"`
	Status          string                      `json:"status" yaml:"status"`
	LastLocalUpdate string                      `json:"last_local_update" yaml:"last_local_update"`
	Remotes         []RbdImageStatusRemoteBrief `json:"remotes" yaml:"remotes"`
}

// Types for Rbd List

type RbdPoolListImageBrief struct {
	Name            string `json:"name" yaml:"name"`
	Type            string `json:"Type" yaml:"Type"`
	IsPrimary       bool   `json:"is_primary" yaml:"is_primary"`
	LastLocalUpdate string `json:"last_local_update" yaml:"last_local_update"`
}

type RbdPoolBrief struct {
	Name   string `json:"name" yaml:"name"`
	Images []RbdPoolListImageBrief
}

type RbdPoolList []RbdPoolBrief

// ################################## RBD Replication Request ##################################

// RbdReplicationDirection defines Rbd mirror direction
type RbdReplicationDirection string

const (
	RbdReplicationDirectionRXOnly RbdReplicationDirection = "rx-only"
	RbdReplicationDirectionRXTX   RbdReplicationDirection = "rx-tx"
)

// RbdResourceType defines request resource type
type RbdResourceType ReplicationResourceType

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
	LocalAlias  string `json:"local_alias" yaml:"local_alias"`
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

// GetAPIObjectID provides the API object id i.e. /replication/rbd/<object-id>
func (req RbdReplicationRequest) GetAPIObjectID() string {
	// If both Pool and Image values are present encode for query.
	if len(req.SourceImage) != 0 && len(req.SourcePool) != 0 {
		resource := url.QueryEscape(fmt.Sprintf("%s/%s", req.SourcePool, req.SourceImage))
		logger.Debugf("REPAPI: Resource: %s", resource)
		return resource
	}

	return req.SourcePool
}

// SetAPIObjectID provides the API object id i.e. /replication/rbd/<object-id>
func (req *RbdReplicationRequest) SetAPIObjectID(id string) error {
	// unescape object string
	object, err := url.PathUnescape(id)
	if err != nil {
		return err
	}

	frags := strings.Split(string(object), "/")
	if len(frags) > 1 {
		req.SourcePool = frags[0]
		req.SourceImage = frags[1]
	} else {
		req.SourcePool = object
	}

	return nil
}

// GetAPIRequestType provides the REST method for the request
func (req RbdReplicationRequest) GetAPIRequestType() string {
	return GetAPIRequestTypeGeneric(req.RequestType)
}

// GetWorkloadRequestType provides the event used as the FSM trigger.
func (req RbdReplicationRequest) GetWorkloadRequestType() string {
	return GetWorkloadRequestTypeGeneric(req.RequestType)
}

// OverwriteRequestType sets the RequestType param to provided value.
func (req *RbdReplicationRequest) OverwriteRequestType(overwriteRequestType ReplicationRequestType) {
	if len(overwriteRequestType) != 0 {
		req.RequestType = overwriteRequestType
	}
}

// ################### Helpers ############################

// GetRbdResourceType gets the resource type of the said request
func GetRbdResourceType(poolName string, imageName string) RbdResourceType {
	if len(poolName) != 0 && len(imageName) != 0 {
		return RbdResourceImage
	} else {
		return RbdResourcePool
	}
}

func GetPoolAndImageFromResource(resource string) (string, string, error) {
	var pool string
	var image string
	resourceFrags := strings.Split(resource, "/")
	if len(resourceFrags) < 1 || len(resourceFrags) > 2 {
		return "", "", fmt.Errorf("check resource name %s, should be in $pool/$image format", resource)
	}

	// If only pool name is provided.
	if len(resourceFrags) == 1 {
		pool = resourceFrags[0]
		image = ""
	} else
	// if both pool and image names are provided.
	if len(resourceFrags) == 2 {
		pool = resourceFrags[0]
		image = resourceFrags[1]
	}

	return pool, image, nil
}
