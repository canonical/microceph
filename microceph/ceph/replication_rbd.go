package ceph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

type RbdReplicationPeer struct {
	Id         string                        `json:"uuid"`
	MirrorId   string                        `json:"mirror_uuid"`
	RemoteName string                        `json:"site_name"`
	Direction  types.RbdReplicationDirection `json:"direction"`
}

type RbdReplicationPoolInfo struct {
	Mode          types.RbdResourceType `json:"mode"`
	LocalSiteName string                `json:"site_name"`
	Peers         []RbdReplicationPeer  `json:"peers"`
}

type RbdReplicationHealth string

const (
	RbdReplicationHealthOK   RbdReplicationHealth = "OK"
	RbdReplicationHealthWarn RbdReplicationHealth = "WARNING"
	RbdReplicationHealthErr  RbdReplicationHealth = "Error"
)

// RbdReplicationPoolStatus does not have tags defined for jason because it needs custom logic.
type RbdReplicationPoolStatus struct {
	State        ReplicationState
	ImageCount   int
	Health       RbdReplicationHealth `json:"health" yaml:"health"`
	DaemonHealth RbdReplicationHealth `json:"daemon_health" yaml:"daemon_health"`
	ImageHealth  RbdReplicationHealth `json:"image_health" yaml:"image_health"`
	Description  string               `yaml:"images"` // only un/marshal if from yaml format.
}

type RbdReplicationVerbosePoolStatus struct {
	Summary RbdReplicationPoolStatus  `json:"summary"`
	Images  RbdReplicationImageStatus `json:"images"`
}

type RbdReplicationImagePeer struct {
	MirrorId   string `json:"mirror_uuids"`
	RemoteName string `json:"site_name"`
	State      string `json:"state"`
	Status     string `json:"description"`
	LastUpdate string `json:"last_update"`
}

type RbdReplicationImageStatus struct {
	State       ReplicationState // whether replication is enabled or disabled
	isPrimary   bool
	ID          string                    `json:"global_id"`
	Status      string                    `json:"state"`
	LastUpdate  string                    `json:"last_update"`
	Peers       []RbdReplicationImagePeer `json:"peer_sites"`
	Description string                    `json:"description"`
}

type RbdReplicationHandler struct {
	// Resource Info
	PoolInfo    RbdReplicationPoolInfo    `json:"pool_info"`
	PoolStatus  RbdReplicationPoolStatus  `json:"pool_status"`
	ImageStatus RbdReplicationImageStatus `json:"image_status"`
	// Request Info
	Request types.RbdReplicationRequest
}

// PreFill populates the handler struct with requested rbd pool/image information.
func (rh *RbdReplicationHandler) PreFill(ctx context.Context, request types.ReplicationRequest) error {
	var err error
	req := request.(types.RbdReplicationRequest)
	rh.Request = req
	// Populate pool Info
	rh.PoolInfo, err = GetRbdMirrorPoolInfo(req.SourcePool, "", "")
	if err != nil {
		return err
	}

	// Populate pool status
	rh.PoolStatus, err = GetRbdMirrorPoolStatus(req.SourcePool, "", "")
	if err != nil {
		return err
	}

	if req.ResourceType == types.RbdResourceImage {
		// Populate image status
		rh.ImageStatus, err = GetRbdMirrorImageStatus(req.SourcePool, req.SourceImage, "", "")
		return err
	}

	return nil
}

// GetResourceState fetches the mirroring state for requested rbd pool/image.
func (rh *RbdReplicationHandler) GetResourceState() ReplicationState {
	// Image request but mirroring is disabled on image.
	if rh.Request.ResourceType == types.RbdResourceImage {
		return rh.ImageStatus.State
	}

	// Pool request
	return rh.PoolStatus.State
}

// EnableHandler enables mirroring for requested rbd pool/image.
func (rh *RbdReplicationHandler) EnableHandler(ctx context.Context, args ...any) error {
	st := args[repArgState].(interfaces.CephState).ClusterState()
	dbRec, err := database.GetRemoteDb(*st, rh.Request.RemoteName)
	if err != nil {
		errNew := fmt.Errorf("remote (%s) does not exist: %w", rh.Request.RemoteName, err)
		return errNew
	}

	logger.Infof("REPRBD: Local(%s) Remote(%s)", dbRec[0].LocalName, dbRec[0].Name)
	if rh.Request.ResourceType == types.RbdResourcePool {
		return handlePoolEnablement(rh, dbRec[0].LocalName, dbRec[0].Name)
	} else if rh.Request.ResourceType == types.RbdResourceImage {
		return handleImageEnablement(rh, dbRec[0].LocalName, dbRec[0].Name)
	}

	return fmt.Errorf("unknown enable request for rbd mirroring %s", rh.Request.ResourceType)
}

// DisableHandler disables mirroring configured for requested rbd pool/image.
func (rh *RbdReplicationHandler) DisableHandler(ctx context.Context, args ...any) error {
	st := args[repArgState].(interfaces.CephState).ClusterState()
	dbRec, err := database.GetRemoteDb(*st, rh.Request.RemoteName)
	if err != nil {
		errNew := fmt.Errorf("remote (%s) does not exist: %w", rh.Request.RemoteName, err)
		return errNew
	}

	logger.Infof("REPRBD: Entered RBD Disable Handler Local(%s) Remote(%s)", dbRec[0].LocalName, dbRec[0].Name)
	if rh.Request.ResourceType == types.RbdResourcePool {
		return handlePoolDisablement(rh, dbRec[0].LocalName, dbRec[0].Name)
	} else if rh.Request.ResourceType == types.RbdResourceImage {
		return handleImageDisablement(rh)
	}

	return fmt.Errorf("unknown disable request for rbd mirroring %s", rh.Request.ResourceType)
}

// ConfigureHandler configures replication properties for requested rbd pool/image.
func (rh *RbdReplicationHandler) ConfigureHandler(ctx context.Context, args ...any) error {
	if rh.Request.Schedule == constants.DisableSnapshotSchedule {
		return configureSnapshotSchedule(rh.Request.SourcePool, rh.Request.SourceImage, "", "")
	}

	schedule, err := getSnapshotSchedule(rh.Request.SourcePool, rh.Request.SourceImage)
	if err != nil {
		return err
	}

	if rh.Request.Schedule != schedule.Schedule {
		return configureSnapshotSchedule(rh.Request.SourcePool, rh.Request.SourceImage, rh.Request.Schedule, "")
	}

	// no change
	return nil
}

// ListHandler fetches a list of rbd pools/images configured for mirroring.
func (rh *RbdReplicationHandler) ListHandler(ctx context.Context, args ...any) error {
	// fetch all ceph pools initialised with rbd application.
	pools := ListPools("rbd")

	// TODO: make this print debug
	logger.Infof("REPRBD: Scan active pools %v", pools)

	// fetch verbose pool status for each pool
	statusList := []RbdReplicationVerbosePoolStatus{}
	for _, pool := range pools {
		poolStatus, err := GetRbdMirrorVerbosePoolStatus(pool.Name, "", "")
		if err != nil {
			logger.Warnf("failed to fetch status for %s pool: %v", pool.Name, err)
			continue
		}

		statusList = append(statusList, poolStatus)
	}

	// TODO: Make this print debug.
	logger.Infof("REPRBD: List Verbose Pool status: %v", statusList)

	resp, err := json.Marshal(statusList)
	if err != nil {
		return fmt.Errorf("failed to marshal response(%v): %v", statusList, err)
	}

	// pass response for API
	*args[repArgResponse].(*string) = string(resp)
	return nil
}

// StatusHandler fetches the status of requested rbd pool/image resource.
func (rh *RbdReplicationHandler) StatusHandler(ctx context.Context, args ...any) error {
	data, err := json.Marshal(rh)
	if err != nil {
		err := fmt.Errorf("failed to marshal resource status: %w", err)
		logger.Error(err.Error())
		return err
	}

	// populate response for API
	*args[repArgResponse].(*string) = string(data)
	return nil
}

// ################### Helper Functions ###################
// Enable handler for pool resource.
func handlePoolEnablement(rh *RbdReplicationHandler, localSite string, remoteSite string) error {
	if rh.PoolInfo.Mode == types.RbdResourcePool {
		return nil // already in pool mode
	} else

	// Fail if in Image mode with Mirroring Images > 0
	if rh.PoolInfo.Mode == types.RbdResourceImage {
		enabledImageCount := rh.PoolStatus.ImageCount
		if enabledImageCount != 0 {
			return fmt.Errorf("pool (%s) in Image mode, Disable %d mirroring Images", rh.Request.SourcePool, enabledImageCount)
		}
	}

	return EnablePoolMirroring(rh.Request.SourcePool, types.RbdResourcePool, localSite, remoteSite)
}

// Enable handler for image resource.
func handleImageEnablement(rh *RbdReplicationHandler, localSite string, remoteSite string) error {
	if rh.PoolInfo.Mode == types.RbdResourceDisabled {
		// Enable pool mirroring in Image mode
		err := EnablePoolMirroring(rh.Request.SourcePool, types.RbdResourceImage, localSite, remoteSite)
		if err != nil {
			logger.Error(err.Error())
			return err
		}
		// continue for Image enablement
	} else if rh.PoolInfo.Mode == types.RbdResourcePool {
		if rh.Request.ReplicationType == types.RbdReplicationJournaling {
			return configureImageFeatures(rh.Request.SourcePool, rh.Request.SourceImage, "enable", "journaling")
		} else {
			return fmt.Errorf("parent pool (%s) enabled in Journaling mode, Image(%s) requested in Snapshot mode", rh.Request.SourcePool, rh.Request.SourceImage)
		}
	}

	// pool in Image mode, Enable Image in requested mode.
	return configureImageMirroring(rh.Request)
}

// Disable handler for pool resource.
func handlePoolDisablement(rh *RbdReplicationHandler, localSite string, remoteSite string) error {
	// Handle Pool already disabled
	if rh.PoolInfo.Mode == types.RbdResourceDisabled {
		return nil
	} else

	// Fail if in Image mode with Mirroring Images > 0
	if rh.PoolInfo.Mode == types.RbdResourceImage {
		enabledImageCount := rh.PoolStatus.ImageCount
		if enabledImageCount != 0 {
			return fmt.Errorf("pool (%s) in Image mode, has %d images mirroring", rh.Request.SourcePool, enabledImageCount)
		}
	}

	// Fail if both sites not healthy
	if rh.PoolStatus.Health != RbdReplicationHealthOK {
		return fmt.Errorf("pool replication status not OK(%s), Can't proceed", rh.PoolStatus.Health)
	}

	return DisablePoolMirroring(rh.Request.SourcePool, rh.PoolInfo.Peers[0], localSite, remoteSite)
}

// Disable handler for image resource.
func handleImageDisablement(rh *RbdReplicationHandler) error {
	// Pool already disabled
	if rh.PoolInfo.Mode == types.RbdResourceDisabled {
		return nil
	}

	// Image already disabled
	if rh.ImageStatus.State == StateDisabledReplication {
		return nil
	}

	if rh.PoolInfo.Mode == types.RbdResourcePool {
		return configureImageFeatures(rh.Request.SourcePool, rh.Request.SourceImage, "disable", "journaling")
	}

	// patch replication type
	rh.Request.ReplicationType = types.RbdReplicationDisabled
	return configureImageMirroring(rh.Request)
}
