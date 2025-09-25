package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// CephFSSnapshotMirrorDaemonStatus is the abstraction for storing
type CephFSSnapshotMirrorDaemonStatus []struct {
	DaemonID    int `json:"daemon_id"`
	Filesystems []struct {
		FilesystemID   int    `json:"filesystem_id"`
		Name           string `json:"name"`
		DirectoryCount int    `json:"directory_count"`
		Peers          []struct {
			UUID   string `json:"uuid"`
			Remote struct {
				ClientName  string `json:"client_name"`
				ClusterName string `json:"cluster_name"`
				FsName      string `json:"fs_name"`
			} `json:"remote"`
			Stats struct {
				FailureCount  int `json:"failure_count"`
				RecoveryCount int `json:"recovery_count"`
			} `json:"stats"`
		} `json:"peers"`
	} `json:"filesystems"`
}

// MirrorStatus represents the mirroring status of a filesystem for all peers
type MirrorStatus map[string]types.CephFsReplicationMirrorStatusMap

// MirrorPathList represent a slice of paths in a volume enabled for mirroring.
type MirrorPathList []string

// MirrorPathMap is a map of volumes to their mirroring resource path lists.
type MirrorPathMap map[Volume]MirrorPathList

type CephfsReplicationHandler struct {
	// Prefill objects: Always populated before any handler is called.
	// Snapshot Mirror Status
	FsMirrorDaemonStatus CephFSSnapshotMirrorDaemonStatus
	// Mirror list of paths
	MirrorList MirrorPathList
	// Request Info
	Request types.CephfsReplicationRequest

	// Only populated during status requests.
	// Mirroring status of a filesystem for all requested peers.
	Status MirrorStatus
}

// PreFill populates the handler struct with requested cephfs volume information.
func (rh *CephfsReplicationHandler) PreFill(ctx context.Context, request types.ReplicationRequest) error {
	var err error
	req := request.(types.CephfsReplicationRequest)
	rh.Request = req

	// fetch snapshot mirror daemon status
	rh.FsMirrorDaemonStatus, err = GetCephFSSnapshotMirrorDaemonStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to get CephFS snapshot mirror status: %w", err)
	}

	// Mandatory requirement for cephfs mirroring.
	if len(rh.FsMirrorDaemonStatus) == 0 {
		return fmt.Errorf("no cephfs-mirror daemon available, enable service")
	}

	if len(rh.Request.Volume) != 0 {
		rh.MirrorList, err = GetCephFSVolumeMirrorList(ctx, rh.Request.Volume)
		if err != nil {
			return fmt.Errorf("failed to get CephFS snapshot mirror list: %w", err)
		}
	}

	// This information is only required for status requests.
	if rh.Request.RequestType == types.StatusReplicationRequest {
		err = rh.GetCephFSMirrorStatus(ctx)
		if err != nil {
			return fmt.Errorf("failed to get CephFS mirror directories: %w", err)
		}
	}

	return nil
}

// GetResourceState fetches the mirroring state for requested cephfs subvolume/directory.
func (rh *CephfsReplicationHandler) GetResourceState() (ReplicationState, error) {
	isVolumeMirrorEnabled := false
	for _, daemon := range rh.FsMirrorDaemonStatus {
		for _, fs := range daemon.Filesystems {
			if fs.Name == rh.Request.Volume {
				isVolumeMirrorEnabled = true
			}
		}
	}

	if !isVolumeMirrorEnabled {
		return StateDisabledReplication, nil
	}

	if rh.Request.ResourceType == types.CephfsResourceSubvolume {
		return GetCephFSSubvolumeMirrorState(rh)
	} else if rh.Request.ResourceType == types.CephfsResourceDirectory {
		return GetCephFSMirrorPathState(rh, rh.Request.DirPath)
	} else if len(rh.Request.DirPath) != 0 || len(rh.Request.Subvolume) != 0 {
		return StateInvalidReplication, fmt.Errorf("invalid resource type (%s) for CephFS replication", rh.Request.ResourceType)
	}

	return StateEnabledReplication, nil
}

// EnableHandler enables mirroring for requested cephfs subvolume/directory.
func (rh *CephfsReplicationHandler) EnableHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPCFS: Enable handler, Req %v", rh.Request)

	st := args[repArgState].(interfaces.CephState)
	err := verifyEnableRequestData(ctx, st, rh.Request)
	if err != nil {
		logger.Errorf("Cannot fulfill %s request, data validation failed for %+v: %v", types.EnableReplicationRequest, rh.Request, err)
		return err
	}

	err = enableMgrModule(constants.MgrModuleMirroring)
	if err != nil {
		return err
	}

	err = enableCephFSVolumeMirror(ctx, rh.Request)
	if err != nil {
		logger.Errorf("Failed to enable mirroring on CephFS volume %s: %v", rh.Request.Volume, err)
		return err
	}

	err = enableCephFSResourceMirror(ctx, rh.Request)
	if err != nil {
		logger.Errorf("Failed to enable mirroring on CephFS resource %+v: %v", rh.Request, err)
		return err
	}

	return nil
}

// DisableHandler disables mirroring configured cephfs subvolume/directory.
func (rh *CephfsReplicationHandler) DisableHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPCFS: Disable handler, Req %v", rh.Request)

	var err error
	switch rh.Request.ResourceType {
	case types.CephfsResourceVolume:
		err = disableCephFSVolumeMirror(ctx, rh.Request, rh.MirrorList)
	case types.CephfsResourceSubvolume:
		err = cephFSSnapshotMirrorRemovePath(ctx, rh.Request.Volume, GetCephFSSubvolumePath(rh.Request.SubvolumeGroup, rh.Request.Subvolume))
	case types.CephfsResourceDirectory:
		err = cephFSSnapshotMirrorRemovePath(ctx, rh.Request.Volume, rh.Request.DirPath)
	default:
		err = fmt.Errorf("REPCFS: Disabe request failed, invalid resource type found (%s)", rh.Request.ResourceType)
	}

	if err != nil {
		err = fmt.Errorf("REPCFS: Failed to disable mirroring on CephFS resource %+v: %w", rh.Request, err)
		logger.Error(err.Error())
		return err
	}

	return nil
}

// ConfigureHandler configures replication properties for requested cephfs subvolume/directory.
func (rh *CephfsReplicationHandler) ConfigureHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPCFS: Configure handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for cephfs", types.ConfigureReplicationRequest)
}

// ListHandler fetches a list of directories configured for the requested FS or all FSs.
func (rh *CephfsReplicationHandler) ListHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPCFS: List handler, Req %v", rh.Request)

	mirroredResources, err := GetCephFsAllVolumeMirrorMap(ctx)
	if err != nil {
		return err
	}

	logger.Debugf("REPCFS: Mirrored resources: %v", mirroredResources)

	response := types.CephFsReplicationResponseList{}
	for key, value := range mirroredResources {
		volResp, err := GetCephFsPerVolumeListResponse(key, value)
		if err != nil {
			return err
		}

		if len(volResp) != 0 {
			response[string(key)] = volResp
		}
	}

	data, err := json.Marshal(response)
	if err != nil {
		return err
	}

	// pass response for API
	*args[repArgResponse].(*string) = string(data)
	return nil
}

// StatusHandler fetches the status of requested cephfs subvolume/directory..
func (rh *CephfsReplicationHandler) StatusHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPCFS: Status handler, Req %v", rh.Request)

	response := types.CephFsReplicationResponseStatus{
		Volume:              rh.Request.Volume,
		MirrorResourceCount: len(rh.MirrorList),
		Peers:               make(map[string]types.CephFsReplicationResponsePeerItem),
	}
	for peer, mirrorMap := range rh.Status {
		response.Peers[peer] = types.CephFsReplicationResponsePeerItem{MirrorStatus: mirrorMap}
	}

	// translate peer UUID to remote cluster name
	for _, daemon := range rh.FsMirrorDaemonStatus {
		for _, fs := range daemon.Filesystems {
			if fs.Name != rh.Request.Volume {
				continue
			}

			// cannot directly modify struct fields in a map, so retrieve, modify and reassign
			for _, peer := range fs.Peers {
				responsePeer, _ := response.Peers[peer.UUID]
				responsePeer.Name = peer.Remote.ClusterName
				response.Peers[peer.UUID] = responsePeer
			}
		}
	}

	data, err := json.Marshal(response)
	if err != nil {
		err := fmt.Errorf("failed to marshal resource status: %w", err)
		logger.Error(err.Error())
		return err
	}

	// pass response for API
	*args[repArgResponse].(*string) = string(data)
	return nil
}

// PromoteHandler is not implemented for cephfs workload.
func (rh *CephfsReplicationHandler) PromoteHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPCFS: Promote handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for cephfs", types.PromoteReplicationRequest)
}

// DemoteHandler is not implemented for cephfs workload.
func (rh *CephfsReplicationHandler) DemoteHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPCFS: Demote handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for cephfs", types.DemoteReplicationRequest)
}

// #### CephFS Mirroring Specific Helpers ####

// GetCephFSMirrorStatus fetches the mirroring status of a filesystem for all requested peers.
func (rh *CephfsReplicationHandler) GetCephFSMirrorStatus(ctx context.Context) error {
	var err error

	// Only populate for status requests
	if rh.Request.RequestType != types.StatusReplicationRequest {
		return fmt.Errorf("%s is not %s", rh.Request.RequestType, types.StatusReplicationRequest)
	}

	volumeId, peers := GetCephFsMirrorVolumeAndPeersId(rh)
	if volumeId < 0 || len(peers) == 0 {
		return fmt.Errorf("no CephFS volume (%d) or peers (%v) found for mirroring status", volumeId, peers)
	}

	// TODO: (utkarshbhatthere):
	// The ceph CLI lacks any way to fetch the status of cephfs mirroring on any resource.
	// MicroCeph uses local admin sockets to get the same, however, we should migrate to a
	// ceph cli based output once it is available upstream.
	// https://github.com/canonical/microceph/issues/620
	cephfsMirrorAdminSock, err := FindCephFsMirrorAdminSockPath()
	if err != nil || len(cephfsMirrorAdminSock) == 0 {
		return fmt.Errorf("failed to find CephFS mirror admin socket: %w", err)
	}

	response := MirrorStatus{}
	for _, peer := range peers {
		// Get the mirror status for each peer
		response[peer], err = GetCephFsMirrorPeerStatus(ctx, cephfsMirrorAdminSock, volumeId, peer)
		if err != nil {
			return fmt.Errorf("failed to get CephFS mirror status for peer %s: %w", peer, err)
		}
	}

	rh.Status = response
	return nil
}

func verifyEnableRequestData(ctx context.Context, s interfaces.CephState, request types.CephfsReplicationRequest) error {
	if len(request.Volume) == 0 {
		return fmt.Errorf("missing CephFS volume name")
	}

	if len(request.RemoteName) == 0 {
		return fmt.Errorf("missing remote cluster name")
	} else {
		// check if a remote with remote name exists.
		remotes, err := database.GetRemoteDb(ctx, s.ClusterState(), request.RemoteName)
		if err != nil {
			logger.Errorf("Failed to query remote %s from db: %v", request.RemoteName, err)
			return err
		}

		found := false
		for _, remote := range remotes {
			if remote.Name == request.RemoteName {
				found = true
				break
			}
		}

		if !found {
			err := fmt.Errorf("Remote %s not found in db", request.RemoteName)
			logger.Error(err.Error())
			return err
		}
	}

	if request.ResourceType != types.CephfsResourceSubvolume && request.ResourceType != types.CephfsResourceDirectory {
		return fmt.Errorf("invalid resource type (%s) for CephFS replication", request.ResourceType)
	}

	if request.ResourceType == types.CephfsResourceSubvolume {
		if len(request.Subvolume) == 0 {
			return fmt.Errorf("missing subvolume name for resource type %s", request.ResourceType)
		}

		// check if subvolume exists in the volume
		vol, err := GetCephFSVolume(Volume(request.Volume))
		if err != nil {
			err = fmt.Errorf("Failed to get CephFS volume %s: %v", request.Volume, err)
			logger.Error(err.Error())
			return err
		}

		if !CephFSSubvolumeExists(vol.Name, request.SubvolumeGroup, request.Subvolume) {
			err := fmt.Errorf("subvolume %s/%s not found in volume %s", request.SubvolumeGroup, request.Subvolume, request.Volume)
			logger.Error(err.Error())
			return err
		}
	}

	if request.ResourceType == types.CephfsResourceDirectory && len(request.DirPath) == 0 {
		return fmt.Errorf("missing directory path for resource type %s", request.ResourceType)
	}

	return nil
}

func enableCephFSVolumeMirror(ctx context.Context, request types.CephfsReplicationRequest) error {
	peerExists, err := cephFSSnapshotMirrorPeerExists(ctx, request.Volume, request.RemoteName)
	if err != nil {
		logger.Errorf("Failed to check if peer %s exists for CephFS volume %s: %v", request.RemoteName, request.Volume, err)
		return err
	}

	if !peerExists {
		// Note: CephFS operates on push replication, hence we need to import a remote ceph
		// user with permissions to write on the remote cluster.
		token, err := cephFSSnapshotMirrorPeerCreate(request.Volume, request.RemoteName, request.RemoteName)
		if err != nil {
			logger.Errorf("Failed to create peer for remote %s on CephFS volume %s: %v", request.RemoteName, request.Volume, err)
			return err
		}

		err = cephFSSnapshotMirrorPeerImport(request.Volume, token)
		if err != nil {
			logger.Errorf("Failed to import peer for remote %s on CephFS volume %s: %v", request.RemoteName, request.Volume, err)
			return err
		}
	}

	err = cephFSSnapshotMirrorEnableVolume(request.Volume)
	if err != nil {
		logger.Errorf("Failed to enable mirroring on CephFS volume %s: %v", request.Volume, err)
		return err
	}

	return nil
}

func enableCephFSResourceMirror(ctx context.Context, request types.CephfsReplicationRequest) error {
	var resourcePath string

	switch request.ResourceType {
	case types.CephfsResourceSubvolume:
		resourcePath = GetCephFSSubvolumePath(request.SubvolumeGroup, request.Subvolume)
	case types.CephfsResourceDirectory:
		resourcePath = request.DirPath
	default:
		return fmt.Errorf("invalid resource type (%s) for CephFS replication", request.ResourceType)
	}

	err := cephFSSnapshotMirrorAddPath(ctx, request.Volume, resourcePath)
	if err != nil {
		err := fmt.Errorf("Failed to enable mirroring on CephFS volume %s: %v", request.Volume, err)
		logger.Error(err.Error())
		return err
	}

	return nil
}

func disableCephFSVolumeMirror(ctx context.Context, request types.CephfsReplicationRequest, mirrorPathList []string) error {
	if !request.IsForceOp {
		err := fmt.Errorf("Disabling it for the volume (%s) may result in data-loss, please use appropriate parmaters", request.Volume)
		return err
	}

	for _, mirrorPath := range mirrorPathList {
		err := cephFSSnapshotMirrorRemovePath(ctx, request.Volume, mirrorPath)
		if err != nil {
			logger.Errorf("Failed to remove mirror path %s on CephFS volume %s: %v", mirrorPath, request.Volume, err)
			return err
		}
	}

	return nil
}

// GetCephFsPerVolumeListResponse prepares a slice of cephfs replication resources.
func GetCephFsPerVolumeListResponse(volume Volume, mirrorList MirrorPathList) ([]types.CephFsReplicationResponseListItem, error) {
	vol, err := GetCephFSVolume(volume)
	if err != nil {
		return nil, fmt.Errorf("failed to get CephFS volume %s: %w", volume, err)
	}

	logger.Debugf("REPCFS: Volume %v mirror list: %v", vol, mirrorList)

	response := make([]types.CephFsReplicationResponseListItem, 0, len(mirrorList))
	for _, path := range mirrorList {
		subvolumegroup, subvolume, err := CephFsSubvolumePathDeconstruct(path)
		if err != nil {
			response = append(response, types.CephFsReplicationResponseListItem{
				ResourcePath: path,
				ResourceType: types.CephfsResourceDirectory,
			})
			continue
		}

		if strings.Contains(subvolumegroup, "nogroup") && slices.Contains(vol.UngroupedSubVolumes, UngroupedSubvolume(subvolume)) {
			response = append(response, types.CephFsReplicationResponseListItem{
				ResourcePath: path,
				ResourceType: types.CephfsResourceSubvolume,
			})
			continue
		}

		svGroup, isPresent := vol.SubvolumeGroups[subvolumegroup]
		if !isPresent {
			response = append(response, types.CephFsReplicationResponseListItem{
				ResourcePath: path,
				ResourceType: types.CephfsResourceDirectory,
			})
			continue
		}

		if !slices.Contains(svGroup.SubVolumes, GroupedSubvolume(subvolume)) {
			response = append(response, types.CephFsReplicationResponseListItem{
				ResourcePath: path,
				ResourceType: types.CephfsResourceDirectory,
			})
			continue
		}

		response = append(response, types.CephFsReplicationResponseListItem{
			ResourcePath: path,
			ResourceType: types.CephfsResourceSubvolume,
		})
	}

	return response, err
}
