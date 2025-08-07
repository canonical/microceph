package ceph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
)

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

type MirrorDirStatus struct {
	State   string `json:"state"`
	Synced  int    `json:"snaps_synced"`
	Deleted int    `json:"snaps_deleted"`
	Renamed int    `json:"snaps_renamed"`
}

type CephfsReplicationHandler struct {
	// Prefill objects: Always populated before any handler is called.
	// Snapshot Mirror Status
	FsMirrorDaemonStatus CephFSSnapshotMirrorDaemonStatus
	// Mirror list of paths
	MirrorList []string
	// Request Info
	Request types.CephfsReplicationRequest

	// Only populated during specific requests.
	// Mirror Directories is a map of paths to their mirroring status.
	MirrorDirs map[string]MirrorDirStatus
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

	rh.MirrorList, err = GetCephFSSSnapshotMirrorList(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to get CephFS snapshot mirror list: %w", err)
	}

	// only gets populated if request is Status request and a resource is provided.
	rh.MirrorDirs, err = GetCephFSMirrorDirs(ctx, rh.Request, rh.FsMirrorDaemonStatus)
	if err != nil {
		return fmt.Errorf("failed to get CephFS mirror directories: %w", err)
	}

	return nil
}

// GetResourceState fetches the mirroring state for requested rbd pool/image.
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
	logger.Debugf("REPCFS: Status handler, Req %v", rh.Request)

	// Marshal to json string
	data, err := json.Marshal(rh)
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
func (rh *CephfsReplicationHandler) PromoteHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPFSM: Promote handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for cephfs", types.PromoteReplicationRequest)
}

func (rh *CephfsReplicationHandler) DemoteHandler(ctx context.Context, args ...any) error {
	logger.Debugf("REPFSM: Demote handler, Req %v", rh.Request)
	return fmt.Errorf("%s not implemented for cephfs", types.DemoteReplicationRequest)
}
