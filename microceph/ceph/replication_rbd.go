package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/canonical/microceph/microceph/logger"
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

type RbdReplicationPoolStatusCmdOutput struct {
	Summary RbdReplicationPoolStatus `json:"summary"`
}

// RbdReplicationPoolStatus does not have tags defined for jason because it needs custom logic.
type RbdReplicationPoolStatus struct {
	State        ReplicationState
	ImageCount   int
	Health       RbdReplicationHealth `json:"health" yaml:"health"`
	DaemonHealth RbdReplicationHealth `json:"daemon_health" yaml:"daemon health"`
	ImageHealth  RbdReplicationHealth `json:"image_health" yaml:"image health"`
	Description  map[string]int       `json:"states"  yaml:"images"`
}

type RbdReplicationVerbosePoolStatus struct {
	Name    string                      `json:"name"`
	Summary RbdReplicationPoolStatus    `json:"summary"`
	Images  []RbdReplicationImageStatus `json:"images"`
}

type RbdReplicationImagePeer struct {
	MirrorId   string `json:"mirror_uuids"`
	RemoteName string `json:"site_name"`
	State      string `json:"state"`
	Status     string `json:"description"`
	LastUpdate string `json:"last_update"`
}

type RbdReplicationImageStatus struct {
	Name        string                    `json:"name"`
	State       ReplicationState          // whether replication is enabled or disabled
	IsPrimary   bool                      // not fetched from json field hence no tag for json.
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
func (rh *RbdReplicationHandler) GetResourceState() (ReplicationState, error) {
	// Image request but mirroring is disabled on image.
	if rh.Request.ResourceType == types.RbdResourceImage {
		return rh.ImageStatus.State, nil
	}

	// Pool request
	return rh.PoolStatus.State, nil
}

// EnableHandler enables mirroring for requested rbd pool/image.
func (rh *RbdReplicationHandler) EnableHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPRBD: Enable handler, Req %v", rh.Request)

	st := args[repArgState].(interfaces.CephState).ClusterState()
	dbRec, err := database.GetRemoteDb(ctx, st, rh.Request.RemoteName)
	if err != nil {
		errNew := fmt.Errorf("remote (%s) does not exist: %w", rh.Request.RemoteName, err)
		return errNew
	}

	logger.Infof("REPRBD: Local(%s) Remote(%s)", dbRec[0].LocalName, dbRec[0].Name)
	if rh.Request.ResourceType == types.RbdResourcePool {
		if rh.Request.ReplicationType == types.RbdReplicationSnapshot {
			return fmt.Errorf("Snapshot-based replication is only supported for individual RBD images, not pools")
		} else {
			return handlePoolEnablement(rh, dbRec[0].LocalName, dbRec[0].Name)
		}
	} else if rh.Request.ResourceType == types.RbdResourceImage {
		return handleImageEnablement(rh, dbRec[0].LocalName, dbRec[0].Name)
	}

	return fmt.Errorf("unknown enable request for rbd mirroring %s", rh.Request.ResourceType)
}

// DisableHandler disables mirroring configured for requested rbd pool/image.
func (rh *RbdReplicationHandler) DisableHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPRBD: Disable handler, Req %v", rh.Request)

	st := args[repArgState].(interfaces.CephState).ClusterState()
	dbRec, err := database.GetRemoteDb(ctx, st, rh.Request.RemoteName)
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
	logger.Debugf("REPRBD: Configure handler, Req %v", rh.Request)

	schedule, err := getSnapshotSchedule(rh.Request.SourcePool, rh.Request.SourceImage)
	if err != nil {
		return err
	}

	if rh.Request.Schedule != schedule.Schedule {
		return configureSnapshotSchedule(rh.Request.SourcePool, rh.Request.SourceImage, rh.Request.Schedule, "")
	}

	return nil
}

// ListHandler fetches a list of rbd pools/images configured for mirroring.
func (rh *RbdReplicationHandler) ListHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPRBD: List handler, Req %v", rh.Request)

	// fetch all ceph pools initialised with rbd application.
	pools := ListPools("rbd")

	logger.Debugf("REPRBD: Scan active pools %v", pools)

	// fetch verbose pool status for each pool
	statusList := types.RbdPoolList{}
	for _, pool := range pools {
		poolStatus, err := GetRbdMirrorVerbosePoolStatus(pool.Name, "", "")
		if err != nil {
			logger.Warnf("failed to fetch status for %s pool: %v", pool.Name, err)
			continue
		}

		images := make([]types.RbdPoolListImageBrief, len(poolStatus.Images))
		for id, image := range poolStatus.Images {
			var rep_type string
			if strings.Contains(image.Description, "snapshot") {
				rep_type = "snapshot"
			} else {
				rep_type = "journaling"
			}
			images[id] = types.RbdPoolListImageBrief{
				Name:            image.Name,
				Type:            rep_type,
				IsPrimary:       image.IsPrimary,
				LastLocalUpdate: image.LastUpdate,
			}
		}

		statusList = append(statusList, types.RbdPoolBrief{
			Name:   pool.Name,
			Images: images,
		})
	}

	logger.Debugf("REPRBD: List Verbose Pool status: %v", statusList)

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
	logger.Debugf("REPRBD: Status handler, Req %v", rh.Request)

	var resp any

	// Populate Status resp.
	if rh.Request.ResourceType == types.RbdResourcePool {
		// handle pool status
		remotes := make([]types.RbdPoolStatusRemoteBrief, len(rh.PoolInfo.Peers))
		for id, remote := range rh.PoolInfo.Peers {
			remotes[id] = types.RbdPoolStatusRemoteBrief{
				Name:      remote.RemoteName,
				Direction: string(remote.Direction),
				UUID:      remote.Id,
			}
		}

		// Also add image info
		resp = types.RbdPoolStatus{
			Name:              rh.Request.SourcePool,
			Type:              string(rh.PoolInfo.Mode),
			HealthReplication: string(rh.PoolStatus.Health),
			HealthImages:      string(rh.PoolStatus.ImageHealth),
			HealthDaemon:      string(rh.PoolStatus.DaemonHealth),
			ImageCount:        rh.PoolStatus.ImageCount,
			Remotes:           remotes,
		}
	} else if rh.Request.ResourceType == types.RbdResourceImage {
		// handle image status
		remotes := make([]types.RbdImageStatusRemoteBrief, len(rh.ImageStatus.Peers))
		for id, remote := range rh.ImageStatus.Peers {
			remotes[id] = types.RbdImageStatusRemoteBrief{
				Name:             remote.RemoteName,
				Status:           remote.Status,
				LastRemoteUpdate: remote.LastUpdate,
			}
		}

		var rep_type string
		if strings.Contains(rh.ImageStatus.Status, "snapshot") {
			rep_type = "snapshot"
		} else {
			rep_type = "journaling"
		}

		resp = types.RbdImageStatus{
			Name:            fmt.Sprintf("%s/%s", rh.Request.SourcePool, rh.Request.SourceImage),
			ID:              rh.ImageStatus.ID,
			Type:            rep_type,
			Status:          rh.ImageStatus.Status,
			LastLocalUpdate: rh.ImageStatus.LastUpdate,
			IsPrimary:       rh.ImageStatus.IsPrimary,
			Remotes:         remotes,
		}
	} else {
		return fmt.Errorf("REPRBD: Unable resource type(%s), cannot find status", rh.Request.ResourceType)
	}

	// Marshal to json string
	data, err := json.Marshal(resp)
	if err != nil {
		err := fmt.Errorf("failed to marshal resource status: %w", err)
		logger.Error(err.Error())
		return err
	}

	// pass response for API
	*args[repArgResponse].(*string) = string(data)
	return nil
}

// PromoteHandler promotes sequentially promote all secondary cluster pools to primary.
func (rh *RbdReplicationHandler) PromoteHandler(ctx context.Context, args ...any) error {
	return handleSiteOp(rh)
}

func (rh *RbdReplicationHandler) DemoteHandler(ctx context.Context, args ...any) error {
	if !rh.Request.IsForceOp {
		return fmt.Errorf("demotion may cause data loss on this cluster. %s", constants.CliForcePrompt)
	}

	return handleSiteOp(rh)
}

// ################### Helper Functions ###################
// Enable handler for pool resource.
func handlePoolEnablement(rh *RbdReplicationHandler, localSite string, remoteSite string) error {
	if rh.PoolInfo.Mode == types.RbdResourcePool {
		return nil // already in pool mirroring mode
	} else

	// Fail if in Image mirroring mode with Mirroring Images > 0
	if rh.PoolInfo.Mode == types.RbdResourceImage {
		enabledImageCount := rh.PoolStatus.ImageCount
		if enabledImageCount != 0 {
			return fmt.Errorf("pool (%s) in Image mirroring mode, Disable %d mirroring Images", rh.Request.SourcePool, enabledImageCount)
		}
	}

	err := EnablePoolMirroring(rh.Request.SourcePool, types.RbdResourcePool, localSite, remoteSite)
	if err != nil {
		return err
	}

	if !rh.Request.SkipAutoEnable {
		// Enable mirroring for all images in pool.
		images := listAllImagesInPool(rh.Request.SourcePool, "", "")
		for _, image := range images {
			err := enableRbdImageFeatures(rh.Request.SourcePool, image, constants.RbdJournalingEnableFeatureSet[:])
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// Enable handler for image resource.
func handleImageEnablement(rh *RbdReplicationHandler, localSite string, remoteSite string) error {
	if rh.PoolInfo.Mode == types.RbdResourceDisabled {
		// Enable pool mirroring in Image mirroring mode
		err := EnablePoolMirroring(rh.Request.SourcePool, types.RbdResourceImage, localSite, remoteSite)
		if err != nil {
			logger.Error(err.Error())
			return err
		}
		// continue for Image enablement
	} else if rh.PoolInfo.Mode == types.RbdResourcePool {
		if rh.Request.ReplicationType == types.RbdReplicationJournaling {
			return enableRbdImageFeatures(rh.Request.SourcePool, rh.Request.SourceImage, constants.RbdJournalingEnableFeatureSet[:])
		} else {
			return fmt.Errorf("parent pool (%s) enabled in Journaling mode, Image(%s) requested in Snapshot mode", rh.Request.SourcePool, rh.Request.SourceImage)
		}
	}

	// pool in Image mirroring mode, Enable Image in requested mode.
	return configureImageMirroring(rh.Request)
}

// Disable handler for pool resource.
func handlePoolDisablement(rh *RbdReplicationHandler, localSite string, remoteSite string) error {
	// Handle Pool already disabled
	if rh.PoolInfo.Mode == types.RbdResourceDisabled {
		return nil
	}

	// Fail if both sites not healthy and not a forced operation.
	if rh.PoolStatus.Health != RbdReplicationHealthOK && !rh.Request.IsForceOp {
		return fmt.Errorf("pool replication status not OK(%s), Can't proceed", rh.PoolStatus.Health)
	}

	// Fail if in Image mirroring mode with Mirroring Images > 0
	if rh.PoolInfo.Mode == types.RbdResourceImage {
		enabledImageCount := rh.PoolStatus.ImageCount
		if enabledImageCount != 0 {
			return fmt.Errorf("pool (%s) in Image mirroring mode, has %d images mirroring", rh.Request.SourcePool, enabledImageCount)
		}
	} else

	// If pool in pool mirroring mode, disable all images.
	if rh.PoolInfo.Mode == types.RbdResourcePool {
		err := DisableAllMirroringImagesInPool(rh.Request.SourcePool)
		if err != nil {
			return err
		}
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
		return disableRbdImageFeatures(rh.Request.SourcePool, rh.Request.SourceImage, []string{"journaling"})
	}

	// patch replication type
	rh.Request.ReplicationType = types.RbdReplicationDisabled
	return configureImageMirroring(rh.Request)
}

func isPeerRegisteredForMirroring(peers []RbdReplicationPeer, peerName string) bool {
	for _, peer := range peers {
		if peer.RemoteName == peerName {
			return true
		}
	}
	return false
}

// getMirrorPoolMetadata fetches pool status and info if mirroring is enabled on pool.
func getMirrorPoolMetadata(poolName string) (RbdReplicationPoolStatus, RbdReplicationPoolInfo, error) {
	poolStatus, err := GetRbdMirrorPoolStatus(poolName, "", "")
	if err != nil {
		logger.Warnf("REPRBD: failed to fetch status for %s pool: %v", poolName, err)
		return RbdReplicationPoolStatus{}, RbdReplicationPoolInfo{}, err
	}

	poolInfo, err := GetRbdMirrorPoolInfo(poolName, "", "")
	if err != nil {
		logger.Warnf("REPRBD: failed to fetch status for %s pool: %v", poolName, err)
		return RbdReplicationPoolStatus{}, RbdReplicationPoolInfo{}, err
	}

	return poolStatus, poolInfo, nil
}

// Promote local pool to primary.
func handlePoolPromotion(poolName string, isforce bool) error {
	err := promotePool(poolName, isforce, "", "")
	if err != nil {
		logger.Errorf("failed to promote pool (%s): %v", poolName, err)

		if strings.Contains(err.Error(), constants.RbdMirrorNonPrimaryPromoteErr) {
			return fmt.Errorf(constants.CliForcePrompt)
		}

		return err
	}
	return nil
}

// Demote local pool to secondary.
func handlePoolDemotion(poolName string) error {
	err := demotePool(poolName, "", "")
	if err != nil {
		logger.Errorf("failed to demote pool (%s): %v", poolName, err)
		return err
	}

	err = ResyncAllMirroringImagesInPool(poolName)
	if err != nil {
		logger.Warnf("failed to trigger resync for pool %s: %v", poolName, err)
		return err
	}
	return nil
}

func handleSiteOp(rh *RbdReplicationHandler) error {
	// fetch all rbd pools.
	pools := ListPools("rbd")

	logger.Debugf("REPRBD: Scan active pools %v", pools)

	// perform requested op per pool
	for _, pool := range pools {
		poolStatus, poolInfo, err := getMirrorPoolMetadata(pool.Name)
		if err != nil {
			ne := fmt.Errorf("failed to fetch pool (%s) metadata: %v", pool.Name, err)
			logger.Errorf(ne.Error())
			return ne
		}

		if poolStatus.State != StateEnabledReplication {
			// mirroring not enabled on rbd pool.
			logger.Infof("REPRBD: pool(%s) is not an rbd mirror pool.", pool.Name)
			continue
		}

		if !isPeerRegisteredForMirroring(poolInfo.Peers, rh.Request.RemoteName) {
			logger.Infof("REPRBD: pool(%s) has no peer(%s), skipping", pool.Name, rh.Request.RemoteName)
			continue
		}

		if rh.Request.RequestType == types.PromoteReplicationRequest {
			err := handlePoolPromotion(pool.Name, rh.Request.IsForceOp)
			if err != nil {
				return err
			}
			// continue to next pool
			continue
		}

		if rh.Request.RequestType == types.DemoteReplicationRequest {
			err := handlePoolDemotion(pool.Name)
			if err != nil {
				return nil
			}
			// continue to next pool
			continue
		}
	}

	return nil
}
