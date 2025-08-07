package ceph

import (
	"context"
	"fmt"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
)

type CephFSSnapshotMirrorStatus []struct {
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

type MirrorDirStatus struct {
	State   string `json:"state"`
	Synced  int    `json:"snaps_synced"`
	Deleted int    `json:"snaps_deleted"`
	Renamed int    `json:"snaps_renamed"`
}

type CephfsReplicationHandler struct {
	// Snapshot Mirror Status
	FsMirrorStatus CephFSSnapshotMirrorStatus
	// Mirror Directories
	MirrorDirs map[string]MirrorDirStatus
	// Request Info
	Request types.CephfsReplicationRequest
}

// PreFill populates the handler struct with requested cephfs volume information.
func (rh *CephfsReplicationHandler) PreFill(ctx context.Context, request types.ReplicationRequest) error {
	var err error
	req := request.(types.CephfsReplicationRequest)
	rh.Request = req

	// fetch snapshot mirror daemon status
	rh.FsMirrorStatus, err = GetCephFSSnapshotMirrorStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to get CephFS snapshot mirror status: %w", err)
	}

	// Mandatory requirement for cephfs mirroring.
	if len(rh.FsMirrorStatus) == 0 {
		return fmt.Errorf("no cephfs-mirror daemon available, enable service")
	}

	// fetch directories under mirroring
	rh.MirrorDirs, err = GetCephFSMirrorDirs(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to get CephFS mirror directories: %w", err)
	}

	return nil
}

// GetResourceState fetches the mirroring state for requested rbd pool/image.
func (rh *CephfsReplicationHandler) GetResourceState() ReplicationState {
	if rh.Request.ResourceType == types.CephfsResourceSubvolume {
		return GetCephFSSubvolumeMirrorState()
	} else if rh.Request.ResourceType == types.CephfsResourceDirectory {
		return GetCephFSDirMirrorState()
	} else {
		return StateInvalidReplication
	}
}

// EnableHandler enables mirroring for requested rbd pool/image.
func (rh *CephfsReplicationHandler) EnableHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPFSM: Enable handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for cephfs", types.EnableReplicationRequest)
}

// DisableHandler disables mirroring configured for requested rbd pool/image.
func (rh *CephfsReplicationHandler) DisableHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPFSM: Disable handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for cephfs", types.DisableReplicationRequest)
}

// ConfigureHandler configures replication properties for requested rbd pool/image.
func (rh *CephfsReplicationHandler) ConfigureHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPFSM: Configure handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for cephfs", types.ConfigureReplicationRequest)
}

// ListHandler fetches a list of rbd pools/images configured for mirroring.
func (rh *CephfsReplicationHandler) ListHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPFSM: List handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for cephfs", types.ListReplicationRequest)
}

// StatusHandler fetches the status of requested rbd pool/image resource.
func (rh *CephfsReplicationHandler) StatusHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPFSM: Status handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for cephfs", types.StatusReplicationRequest)
}

// PromoteHandler promotes sequentially promote all secondary cluster pools to primary.
func (rh *CephfsReplicationHandler) PromoteHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPFSM: Promote handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for cephfs", types.PromoteReplicationRequest)
}

func (rh *CephfsReplicationHandler) DemoteHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPFSM: Demote handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for cephfs", types.DemoteReplicationRequest)
}

// ################### Helper Functions ###################

// GetFSSnapshotMirrorStatus fetches the snapshot mirror status for the CephFS volume.
func GetCephFSSnapshotMirrorStatus(ctx context.Context) (CephFSSnapshotMirrorStatus, error) {
	return nil, nil // TODO: Implement this function to fetch CephFS snapshot mirror status.
}

// GetCephFSMirrorDirs fetches the directories under mirroring for the CephFS volume.
func GetCephFSMirrorDirs(ctx context.Context, req types.CephfsReplicationRequest) (map[string]MirrorDirStatus, error) {
	return nil, nil // TODO: Implement this function to fetch CephFS mirror directories.
}

// GetCephFSSubvolumeMirrorStatea fetches the subvolume mirroring state for the CephFS volume.
func GetCephFSSubvolumeMirrorState() ReplicationState {
	return StateDisabledReplication // TODO: Implement logic to determine the state of CephFS subvolume mirroring.
}

// GetCephFSDirMirrorState fetches the directory mirroring state for the CephFS volume.
func GetCephFSDirMirrorState() ReplicationState {
	return StateDisabledReplication // TODO: Implement logic to determine the state of CephFS directory mirroring.
}
