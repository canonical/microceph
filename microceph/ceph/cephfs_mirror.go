package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/logger"
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

// GetCephFSVolumeMirrorList fetches the list of paths enabled for mirroring in a volume.
func GetCephFSVolumeMirrorList(ctx context.Context, volume string) (MirrorPathList, error) {
	if len(volume) == 0 {
		return nil, fmt.Errorf("volume name cannot be empty")
	}

	args := []string{"fs", "snapshot", "mirror", "ls", volume, "--format=json"}
	output, err := cephRunContext(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get CephFS snapshot mirror list: %w", err)
	}

	response := MirrorPathList{}
	err = json.Unmarshal([]byte(output), &response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CephFS snapshot mirror list: %w", err)
	}

	return response, nil
}

func GetCephFsAllVolumeMirrorMap(ctx context.Context) (MirrorPathMap, error) {
	volumes, err := ListCephFSVolumes()
	if err != nil {
		return nil, fmt.Errorf("failed to list CephFS volumes: %w", err)
	}

	mirroredResources := MirrorPathMap{}
	for _, volume := range volumes {
		paths, err := GetCephFSVolumeMirrorList(ctx, fmt.Sprintf("%s", volume))
		if err != nil {
			return mirroredResources, fmt.Errorf("failed to capture cephfs mirror list for %v: %w", volumes, err)
		}

		if len(paths) != 0 {
			mirroredResources[volume] = paths
		}
	}

	return mirroredResources, nil
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

// GetCephFsMirrorPeerStatus fetches the per directory mirroring status for a given CephFS volume and remote.
func GetCephFsMirrorPeerStatus(ctx context.Context, adminSockPath string, volumeId int, peerId string) (types.CephFsReplicationMirrorStatusMap, error) {
	args := []string{
		"--admin-daemon", adminSockPath,
		"fs", "mirror", "peer", "status",
		fmt.Sprintf("vol@%d", volumeId),
		peerId,
	}
	output, err := cephRun(args...)
	if err != nil {
		return nil, fmt.Errorf("failed to run ceph admin socket command: %w", err)
	}

	response := types.CephFsReplicationMirrorStatusMap{}
	err = json.Unmarshal([]byte(output), &response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CephFS mirror directories: %w", err)
	}

	return response, nil
}

// Some CephFSMirror commands can only work with admin socket.
// This function finds that path by testing them and returns the correct one.
func FindCephFsMirrorAdminSockPath() (string, error) {
	run_path := constants.GetPathConst().RunPath

	open_sockets, err := os.ReadDir(run_path)
	if err != nil {
		logger.Errorf("failed to read run path %s: %v", run_path, err)
		return "", err
	}

	logger.Debugf("MIRCFS: found %d open sockets in %s", len(open_sockets), run_path)
	for _, socket := range open_sockets {
		logger.Debugf("MIRCFS: checking file : %s", socket.Name())

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

// Executes "help" on admin socket to verify the correctness.
func CheckFsMirrorHelperCommands(admin_socket string) error {
	args := []string{"--admin-daemon", admin_socket, "help"}
	output, err := cephRun(args...)
	if err != nil {
		return fmt.Errorf("failed to run ceph admin socket command: %w", err)
	}

	logger.Debugf("MIRCFS: admin socket help output: %s", output)

	commands := map[string]string{}
	err = json.Unmarshal([]byte(output), &commands)
	if err != nil {
		return fmt.Errorf("failed to parse ceph admin socket command output: %w", err)
	}

	for key, value := range commands {
		logger.Debugf("MIRCFS: Found command: %s, description: %s", key, value)

		if strings.Contains(key, "fs mirror") {
			logger.Debugf("MIRCFS: Found corrrect sock, supports command: %s", key)
			return nil
		}
	}

	return fmt.Errorf("failed: %s is not a compliant sock", admin_socket)
}

// GetCephFsMirrorVolumeAndPeersId fetches the volume ID and peer UUIDs.
// Peers is either a single element slice for peer matching remote name or a slice of all peer UUIDs.
func GetCephFsMirrorVolumeAndPeersId(rh *CephfsReplicationHandler) (int, []string) {
	volumeId := -1
	var peers []string

	for _, daemon := range rh.FsMirrorDaemonStatus {
		for _, fs := range daemon.Filesystems {
			if fs.Name == rh.Request.Volume {
				volumeId = fs.FilesystemID
				peers = make([]string, 0, len(fs.Peers))
				logger.Errorf("filesystemID %d and peers %v", volumeId, fs.Peers)
				for _, peer := range fs.Peers {
					if len(rh.Request.RemoteName) == 0 {
						logger.Errorf("ALL PEERS: now %s", peer.UUID)
						peers = append(peers, peer.UUID)
					} else {
						if peer.Remote.ClusterName == rh.Request.RemoteName {
							logger.Errorf("PEER MATCH: now %s", peer.UUID)
							peers = append(peers, peer.UUID)
							break
						}
					}
				}
			}
		}
	}

	return volumeId, peers
}
