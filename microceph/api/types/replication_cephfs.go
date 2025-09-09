package types

import (
	"net/url"

	"github.com/canonical/microceph/microceph/logger"
)

// ################################## CephFS Replication Request ##################################

// CephfsResourceType defines the resource type for CephFS replication requests.
type CephfsResourceType ReplicationResourceType

const (
	// CephfsResourceSubvolume represents a CephFS subvolume.
	CephfsResourceSubvolume CephfsResourceType = "subvolume"
	// CephfsResourceDirectory represents a directory path in a CephFS volume.
	CephfsResourceDirectory CephfsResourceType = "directory"
	// CephfsResourceInvalid represents an invalid resource type.
	CephfsResourceInvalid CephfsResourceType = "invalid"
)

// CephfsReplicationRequest implements ReplicationRequest for RBD replication.
type CephfsReplicationRequest struct {
	Volume string `json:"volume" yaml:"volume"`
	// A cephfs resource could either be a directory path or a subvolume.
	DirPath string `json:"dir_path" yaml:"dir_path"`
	// Subvolume *MAY* be a part of a subvolume group.
	Subvolume      string `json:"subvolume" yaml:"subvolume"`
	SubvolumeGroup string `json:"subvolume_group" yaml:"subvolume_group"`
	RemoteName     string `json:"remote" yaml:"remote"`
	// Subvolume or Directory Path
	ResourceType CephfsResourceType     `json:"resource_type" yaml:"resource_type"`
	RequestType  ReplicationRequestType `json:"request_type" yaml:"request_type"`
	// snapshot in d,h,m format
	Schedule        string `json:"schedule" yaml:"schedule"`
	RetentionPolicy string `json:"retention_policy" yaml:"retention_policy"`
	IsForceOp       bool   `json:"force" yaml:"force"`
}

// GetWorkloadType provides the workload name for replication request
func (req CephfsReplicationRequest) GetWorkloadType() CephWorkloadType {
	logger.Debugf("REPAPI: Workload: cephfs")
	return CephFsWorkload
}

// GetAPIObjectID provides the API object id i.e. /replication/cephfs/<volume-name>
func (req CephfsReplicationRequest) GetAPIObjectID() string {
	// For filesystem workloads, the only resource is the volume name.
	if len(req.Volume) != 0 {
		logger.Debugf("REPAPI: Resource: %s", req.Volume)
		return req.Volume
	}

	return ""
}

// SetAPIObjectID provides the API object id i.e. /replication/rbd/<object-id>
func (req *CephfsReplicationRequest) SetAPIObjectID(id string) error {
	// unescape object string
	volume, err := url.PathUnescape(id)
	if err != nil {
		return err
	}

	req.Volume = volume
	return nil
}

// GetAPIRequestType provides the REST method for the request
func (req CephfsReplicationRequest) GetAPIRequestType() string {
	return GetAPIRequestTypeGeneric(req.RequestType)
}

// GetWorkloadRequestType provides the event used as the FSM trigger.
func (req CephfsReplicationRequest) GetWorkloadRequestType() string {
	return GetWorkloadRequestTypeGeneric(req.RequestType)
}

// OverwriteRequestType overwrites the request type of the replication request.
func (req *CephfsReplicationRequest) OverwriteRequestType(overwriteRequestType ReplicationRequestType) {
	if len(overwriteRequestType) != 0 {
		req.RequestType = overwriteRequestType
	}
}

// ################################## CephFS Replication Response ##################################

// LIST response

// CephFsReplicationResponseListItem represents a single item in the list response
type CephFsReplicationResponseListItem struct {
	ResourcePath string             `json:"resource_path" yaml:"resource_path"`
	ResourceType CephfsResourceType `json:"resource_type" yaml:"resource_type"`
}

// CephFsReplicationResponseList is a map of volumes to a slice of cephfs mirror resources.
type CephFsReplicationResponseList map[string][]CephFsReplicationResponseListItem

// STATUS response

type CephFsReplicationDirMirrorStatus struct {
	State   string `json:"state"`
	Synced  int    `json:"snaps_synced"`
	Deleted int    `json:"snaps_deleted"`
	Renamed int    `json:"snaps_renamed"`
}

type CephFsReplicationMirrorStatusMap map[string]CephFsReplicationDirMirrorStatus

type CephFsReplicationResponsePeerItem struct {
	Name         string                                      `json:"name" yaml:"name"`
	MirrorStatus map[string]CephFsReplicationDirMirrorStatus `json:"mirror_status" yaml:"mirror_status"`
}

type CephFsReplicationResponseStatus struct {
	Volume              string                                       `json:"volume" yaml:"volume"`
	MirrorResourceCount int                                          `json:"mirror_path_count" yaml:"mirror_path_count"`
	Peers               map[string]CephFsReplicationResponsePeerItem `json:"peers" yaml:"peers"`
}

// ############################ Helper Functions  ##################################

// GetCephfsResourceType gets the resource type of the said request.
func GetCephfsResourceType(subvolume string, dirpath string) CephfsResourceType {
	// only one of subvolume or dirpath should be set.
	if len(subvolume) != 0 && len(dirpath) == 0 {
		return CephfsResourceSubvolume
	} else if len(subvolume) == 0 && len(dirpath) != 0 {
		return CephfsResourceDirectory
	} else {
		return CephfsResourceInvalid
	}
}
