package types

import (
	"net/url"

	"github.com/canonical/lxd/shared/logger"
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
	Volume     string `json:"volume" yaml:"volume"`
	Subvolume  string `json:"subvolume" yaml:"subvolume"`
	DirPath    string `json:"dir_path" yaml:"dir_path"`
	RemoteName string `json:"remote" yaml:"remote"`
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

// GetAPIObjectId provides the API object id i.e. /replication/cephfs/<volume-name>
func (req CephfsReplicationRequest) GetAPIObjectId() string {
	// For filesystem workloads, the only resource is the volume name.
	if len(req.Volume) != 0 {
		logger.Debugf("REPAPI: Resource: %s", req.Volume)
		return req.Volume
	}

	return ""
}

// SetAPIObjectId provides the API object id i.e. /replication/rbd/<object-id>
func (req *CephfsReplicationRequest) SetAPIObjectId(id string) error {
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

// ####################### Helpers #######################
// GetWorkloadResourceType gets the resource type of the said request.
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
