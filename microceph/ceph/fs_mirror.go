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

// GetFSSnapshotMirrorStatus fetches the snapshot mirror status for the CephFS volume.
func GetCephFSSnapshotMirrorStatus(ctx context.Context) (CephFSSnapshotMirrorStatus, error) {
	response := CephFSSnapshotMirrorStatus{}
	args := []string{"fs", "snapshot", "mirror", "daemon", "status", "--format=json"}
	output, err := cephRun(args...)
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
