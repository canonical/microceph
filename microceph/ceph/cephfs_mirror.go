package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/constants"
)

func bootstrapFsMirror(hostname string, path string) error {
	args := []string{
		"auth",
		"get-or-create",
		fmt.Sprintf("client.cephfs-mirror.%s", hostname),
		"mds", "allow r",
		"mgr", "allow r",
		"mon", "profile cephfs-mirror",
		"osd", "allow rw tag cephfs metadata=*, allow r tag cephfs data=*",
		"-o", filepath.Join(path, "keyring"),
	}

	_, err := cephRun(args...)
	if err != nil {
		logger.Errorf("failed to bootstrap cephfs-mirror daemon: %s", err.Error())
		return err
	}

	return nil
}

// ###### Replication Helpers ######

// GetCephFSSnapshotMirrorDaemonStatus fetches the snapshot mirror status for the CephFS volume.
func GetCephFSSnapshotMirrorDaemonStatus(ctx context.Context) (CephFSSnapshotMirrorDaemonStatus, error) {
	response := CephFSSnapshotMirrorDaemonStatus{}
	args := []string{"fs", "snapshot", "mirror", "daemon", "status", "--format=json"}
	output, err := cephRunContext(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get CephFS snapshot mirror status: %w", err)
	}

	logger.Debugf("CephFS snapshot mirror status output: %s", string(output))

	err = json.Unmarshal([]byte(output), &response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CephFS snapshot mirror status: %w", err)
	}

	logger.Debugf("CephFS snapshot mirror status: %+v", response)

	return response, nil
}

// GetCephFSSSnapshotMirrorList fetches the list of paths enabled for mirroring in a volume.
func GetCephFSSSnapshotMirrorList(ctx context.Context, req types.CephfsReplicationRequest) ([]string, error) {
	args := []string{"fs", "snapshot", "mirror", "ls", req.Volume, "--format=json"}
	output, err := cephRunContext(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get CephFS snapshot mirror list: %w", err)
	}

	response := []string{}
	err = json.Unmarshal([]byte(output), &response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CephFS snapshot mirror list: %w", err)
	}

	return response, nil
}

// GetCephFSMirrorDirs fetches the directories under mirroring for the CephFS volume.
func GetCephFSMirrorDirs(ctx context.Context, req types.CephfsReplicationRequest, daemonStatus CephFSSnapshotMirrorDaemonStatus) (map[string]MirrorDirStatus, error) {
	// Only populate for status requests
	if req.RequestType != types.StatusReplicationRequest {
		return nil, nil
	}

	if len(req.DirPath) == 0 && len(req.Subvolume) == 0 {
		return nil, nil
	}

	volumeId := -1
	peerId := ""
	for _, daemon := range daemonStatus {
		for _, fs := range daemon.Filesystems {
			if fs.Name == req.Volume {
				volumeId = fs.FilesystemID
				for _, peer := range fs.Peers {
					if peer.Remote.ClusterName == req.RemoteName {
						peerId = peer.UUID
					}
				}
			}
		}
	}

	if volumeId < 0 || len(peerId) == 0 {
		return nil, fmt.Errorf("failed to fetch mirror Dirs for volume(%s) and remote(%s): deduced", req.Volume, req.RemoteName)
	}

	cephfsMirrorAdminSock, err := FindCephFsMirrorAdminSockPath()
	if err != nil {
		return nil, fmt.Errorf("failed to find CephFS mirror admin socket: %w", err)
	}

	args := []string{
		"--daemon-path", cephfsMirrorAdminSock,
		"fs", "mirror", "peer", "status",
		fmt.Sprintf("vol@%d", volumeId),
		peerId,
	}
	output, err := cephRun(args...)
	if err != nil {
		return nil, fmt.Errorf("failed to run ceph admin socket command: %w", err)
	}

	response := map[string]MirrorDirStatus{}
	err = json.Unmarshal([]byte(output), &response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CephFS mirror directories: %w", err)
	}

	return response, nil
}

// GetCephFSSubvolumeMirrorState fetches the subvolume mirroring state for the CephFS volume.
func GetCephFSSubvolumeMirrorState(rh *CephfsReplicationHandler) (ReplicationState, error) {
	subvolumegroup := rh.Request.SubvolumeGroup
	subvolume := rh.Request.Subvolume
	volume := rh.Request.Volume

	subvolumePath, err := GetCephFSSubvolumePath(volume, subvolumegroup, subvolume)
	if err != nil {
		return StateInvalidReplication, err
	}

	return GetCephFSMirrorPathState(rh, subvolumePath)
}

// GetCephFSMirrorPathState checks whether requested path is in mirror paths list
func GetCephFSMirrorPathState(rh *CephfsReplicationHandler, path string) (ReplicationState, error) {
	for _, mirrorPath := range rh.MirrorList {
		if path == mirrorPath {
			return StateEnabledReplication, nil
		}
	}

	return StateDisabledReplication, nil
}

// ###### Low Level Helpers ######
// Some CephFSMirror commands can only work with admin socket.
// This function finds that path by testing them and returns the correct one.
func FindCephFsMirrorAdminSockPath() (string, error) {
	run_path := constants.GetPathConst().RunPath

	open_sockets, err := os.ReadDir(run_path)
	if err != nil {
		logger.Errorf("failed to read run path %s: %v", run_path, err)
		return "", nil
	}

	for _, socket := range open_sockets {
		if !strings.Contains(socket.Name(), "ceph-client.cephfs-mirror") {
			continue
		}

		full_sock_path := filepath.Join(run_path, socket.Name())
		err = CheckFsMirrorHelperCommands(full_sock_path)
		if err != nil {
			continue
		}

		return full_sock_path, nil
	}

	return "", nil
}

func CheckFsMirrorHelperCommands(admin_socket string) error {
	args := []string{"--daemon-path", admin_socket, "help"}
	output, err := cephRun(args...)
	if err != nil {
		return fmt.Errorf("failed to run ceph admin socket command: %w", err)
	}

	commands := map[string]string{}
	err = json.Unmarshal([]byte(output), &commands)
	if err != nil {
		return fmt.Errorf("failed to parse ceph admin socket command output: %w", err)
	}

	for key, value := range commands {
		logger.Debugf("Found command: %s, description: %s", key, value)

		if strings.Contains(key, "fs mirror") {
			logger.Debugf("Found corrrect sock, supports command: %s", key)
			return nil
		}
	}

	return fmt.Errorf("failed: %s is not a compliant sock", admin_socket)
}
