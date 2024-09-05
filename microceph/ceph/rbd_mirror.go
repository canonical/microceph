package ceph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/constants"
	"gopkg.in/yaml.v2"
)

type imageSnapshotSchedule struct {
	Schedule  string `json:"interval" yaml:"interval"`
	StartTime string `json:"start_time" yaml:"start_time"`
}

// Ceph Commands

// GetRbdMirrorPoolInfo fetches the mirroring info for the requested pool
func GetRbdMirrorPoolInfo(pool string, cluster string, client string) (RbdReplicationPoolInfo, error) {
	response := RbdReplicationPoolInfo{}
	args := []string{"mirror", "pool", "info", pool, "--format", "json"}

	// add --cluster and --id args
	args = appendRemoteClusterArgs(args, cluster, client)

	output, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Warnf("failed info operation on res(%s): %v", pool, err)
		return RbdReplicationPoolInfo{Mode: types.RbdResourceDisabled}, nil
	}

	err = json.Unmarshal([]byte(output), &response)
	if err != nil {
		ne := fmt.Errorf("cannot unmarshal rbd response: %v", err)
		logger.Errorf(ne.Error())
		return RbdReplicationPoolInfo{Mode: types.RbdResourceDisabled}, ne
	}

	// TODO: Make this print debug.
	logger.Infof("REPRBD: Pool Info: %v", response)

	return response, nil
}

// GetRbdMirrorPoolStatus fetches mirroring status for requested pool
func GetRbdMirrorPoolStatus(pool string, cluster string, client string) (RbdReplicationPoolStatus, error) {
	response := RbdReplicationPoolStatus{}
	args := []string{"mirror", "pool", "status", pool}

	output, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Warnf("failed info operation on res(%s): %v", pool, err)
		return RbdReplicationPoolStatus{State: StateDisabledReplication}, nil
	}

	err = yaml.Unmarshal([]byte(output), &response)
	if err != nil {
		ne := fmt.Errorf("cannot unmarshal rbd response: %v", err)
		logger.Errorf(ne.Error())
		return RbdReplicationPoolStatus{State: StateDisabledReplication}, ne
	}

	// TODO: Make this print debug.
	logger.Infof("REPRBD: Pool Status: %v", response)

	// Patch required values
	response.State = StateEnabledReplication
	// "images: 0 total", split value for "images".
	response.ImageCount, err = strconv.Atoi(strings.Split(response.Description, " ")[0])
	if err != nil {
		return RbdReplicationPoolStatus{}, fmt.Errorf("failed to convert %s to int: %w", response.Description, err)
	}

	return response, nil
}

// GetRbdMirrorVerbosePoolStatus fetches mirroring status for requested pool
func GetRbdMirrorVerbosePoolStatus(pool string, cluster string, client string) (RbdReplicationVerbosePoolStatus, error) {
	response := RbdReplicationVerbosePoolStatus{}
	args := []string{"mirror", "pool", "status", pool, "--verbose", "--format", "json"}

	output, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Warnf("failed info operation on res(%s): %v", pool, err)
		return RbdReplicationVerbosePoolStatus{Summary: RbdReplicationPoolStatus{State: StateDisabledReplication}}, nil
	}

	// TODO: Make this print debug.
	logger.Infof("REPRBD: Raw Pool Verbose Status: %s", output)

	err = yaml.Unmarshal([]byte(output), &response)
	if err != nil {
		ne := fmt.Errorf("cannot unmarshal rbd response: %v", err)
		logger.Errorf(ne.Error())
		return RbdReplicationVerbosePoolStatus{Summary: RbdReplicationPoolStatus{State: StateDisabledReplication}}, ne
	}

	// TODO: Make this print debug.
	logger.Infof("REPRBD: Pool Verbose Status: %v", response)

	return response, nil
}

// GetRbdMirrorImageStatus fetches mirroring status for reqeusted image
func GetRbdMirrorImageStatus(pool string, image string, cluster string, client string) (RbdReplicationImageStatus, error) {
	resource := fmt.Sprintf("%s/%s", pool, image)
	response := RbdReplicationImageStatus{}
	args := []string{"mirror", "image", "status", resource, "--format", "json"}

	output, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Warnf("failed info operation on res(%s): %v", resource, err)
		return RbdReplicationImageStatus{State: StateDisabledReplication}, nil
	}

	err = json.Unmarshal([]byte(output), &response)
	if err != nil {
		ne := fmt.Errorf("cannot unmarshal rbd response: %v", err)
		logger.Errorf(ne.Error())
		return RbdReplicationImageStatus{State: StateDisabledReplication}, ne
	}

	// TODO: Make this print debug.
	logger.Infof("REPRBD: Image Status: %v", response)

	// Patch required values
	response.State = StateEnabledReplication
	response.isPrimary = strings.Contains(response.Description, "local image is primary")

	return response, nil
}

func EnablePoolMirroring(pool string, mode types.RbdResourceType, localName string, remoteName string) error {
	// Enable pool mirroring on the local cluster.
	err := configurePoolMirroring(pool, mode, "", "")
	if err != nil {
		return err
	}

	// Enable pool mirroring on the remote cluster.
	err = configurePoolMirroring(pool, mode, localName, remoteName)
	if err != nil {
		return err
	}

	// bootstrap peer
	return BootstrapPeer(pool, localName, remoteName)
}

func DisablePoolMirroring(pool string, peer RbdReplicationPeer, localName string, remoteName string) error {
	// remove peer permissions
	err := RemovePeer(pool, localName, remoteName)
	if err != nil {
		logger.Error(err.Error())
		return err
	}

	// Disable pool mirroring on the local cluster.
	err = configurePoolMirroring(pool, types.RbdResourceDisabled, "", "")
	if err != nil {
		logger.Error(err.Error())
		return err
	}

	// Enable pool mirroring on the remote cluster.
	err = configurePoolMirroring(pool, types.RbdResourceDisabled, localName, remoteName)
	if err != nil {
		logger.Error(err.Error())
		return err
	}

	return nil
}

// getPeerUUID returns the peer ID for the requested peer name.
func getPeerUUID(pool string, peerName string, client string, cluster string) string {
	poolInfo, err := GetRbdMirrorPoolInfo(pool, cluster, client)
	if err != nil {
		logger.Error(err.Error())
		return ""
	}

	for _, peer := range poolInfo.Peers {
		if peer.RemoteName == peerName {
			return peer.Id
		}
	}

	return ""
}

// RemovePeer removes the rbd-mirror peer permissions for requested pool.
func RemovePeer(pool string, localName string, remoteName string) error {
	// find local site's peer with name $remoteName
	localPeer := getPeerUUID(pool, remoteName, "", "")
	remotePeer := getPeerUUID(pool, localName, localName, remoteName)

	// Remove local cluster's peer
	err := peerRemove(pool, localPeer, "", "")
	if err != nil {
		logger.Error(err.Error())
		return err
	}

	// Remove remote's peer
	err = peerRemove(pool, remotePeer, localName, remoteName)
	if err != nil {
		logger.Error(err.Error())
		return err
	}

	return nil
}

// BootstrapPeer bootstraps the rbd-mirror peer permissions for requested pool.
func BootstrapPeer(pool string, localName string, remoteName string) error {
	// create bootstrap token on local site.
	token, err := peerBootstrapCreate(pool, "", localName)
	if err != nil {
		logger.Error(err.Error())
		return err
	}

	// persist the peer token
	err = writeRemotePeerToken(token, remoteName)
	if err != nil {
		logger.Error(err.Error())
		return err
	}

	// import peer token on remote site
	return peerBootstrapImport(pool, localName, remoteName)
}

// ############################# Ceph Commands #############################
func configurePoolMirroring(pool string, mode types.RbdResourceType, localName string, remoteName string) error {
	var args []string
	if mode == types.RbdResourceDisabled {
		args = []string{"mirror", "pool", "disable", pool}
	} else {
		args = []string{"mirror", "pool", "enable", pool, string(mode)}
	}

	// add --cluster and --id args
	args = appendRemoteClusterArgs(args, remoteName, localName)

	_, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		return fmt.Errorf("failed to execute rbd command: %v", err)
	}

	return nil
}

// configureImageMirroring disables or enables image mirroring in requested mode.
func configureImageMirroring(req types.RbdReplicationRequest) error {
	pool := req.SourcePool
	image := req.SourceImage
	mode := req.ReplicationType
	schedule := req.Schedule
	var args []string

	if mode == types.RbdReplicationDisabled {
		args = []string{"mirror", "image", "disable", fmt.Sprintf("%s/%s", pool, image)}
	} else {
		args = []string{"mirror", "image", "enable", fmt.Sprintf("%s/%s", pool, image), string(mode)}
	}

	_, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		return fmt.Errorf("failed to configure rbd image feature: %v", err)
	}

	if mode == types.RbdReplicationSnapshot {
		err = createSnapshot(pool, image)
		if err != nil {
			return fmt.Errorf("failed to create image(%s/%s) snapshot : %v", pool, image, err)
		}

		err = configureSnapshotSchedule(pool, image, schedule, "")
		if err != nil {
			return fmt.Errorf("failed to create image(%s/%s) snapshot schedule(%s) : %v", pool, image, schedule, err)
		}
	}

	return nil
}

func getSnapshotSchedule(pool string, image string) (imageSnapshotSchedule, error) {
	if len(pool) == 0 || len(image) == 0 {
		return imageSnapshotSchedule{}, fmt.Errorf("ImageName(%s/%s) not complete", pool, image)
	}

	output, err := listSnapshotSchedule(pool, image)
	if err != nil {
		return imageSnapshotSchedule{}, err
	}

	ret := []imageSnapshotSchedule{}
	err = json.Unmarshal(output, &ret)
	if err != nil {
		return imageSnapshotSchedule{}, nil
	}

	return ret[0], nil
}

func listSnapshotSchedule(pool string, image string) ([]byte, error) {
	args := []string{"mirror", "snapshot", "schedule", "list"}

	if len(pool) != 0 {
		args = append(args, "--pool")
		args = append(args, pool)
	}

	if len(image) != 0 {
		args = append(args, "--image")
		args = append(args, image)
	}

	output, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		return []byte(""), err
	}

	return []byte(output), nil
}

func configureSnapshotSchedule(pool string, image string, schedule string, startTime string) error {
	var args []string
	if len(schedule) == 0 {
		args = []string{"mirror", "snapshot", "schedule", "rm", "--pool", pool}
	} else {
		args = []string{"mirror", "snapshot", "schedule", "add", "--pool", pool}
	}

	if len(image) != 0 {
		args = append(args, "--image")
		args = append(args, image)
	}

	if len(schedule) != 0 {
		args = append(args, schedule)

		// Also add start-time param if provided.
		if len(startTime) != 0 {
			args = append(args, startTime)
		}
	}

	_, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		return err
	}

	return nil
}

// createSnapshot creates a snapshot of the requested image
func createSnapshot(pool string, image string) error {
	args := []string{"mirror", "image", "snapshot", fmt.Sprintf("%s/%s", pool, image)}

	_, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		return err
	}

	return nil
}

// configureImageFeatures disables or enables requested feature on rbd image.
func configureImageFeatures(pool string, image string, op string, feature string) error {
	// op is enable or disable
	args := []string{"feature", op, fmt.Sprintf("%s/%s", pool, image), feature}

	_, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		return fmt.Errorf("failed to configure rbd image feature: %v", err)
	}

	return nil
}

// peerBootstrapCreate generates peer bootstrap token on remote ceph cluster.
func peerBootstrapCreate(pool string, client string, cluster string) (string, error) {
	args := []string{
		"mirror", "pool", "peer", "bootstrap", "create", "--site-name", cluster, pool,
	}

	// add --cluster and --id args if remote op.
	if len(client) != 0 {
		args = appendRemoteClusterArgs(args, cluster, client)
	}

	output, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		return "", fmt.Errorf("failed to bootstrap peer token: %v", err)
	}

	return output, nil
}

// peerBootstrapImport imports the bootstrap peer on the local cluster using a tokenfile.
func peerBootstrapImport(pool string, client string, cluster string) error {
	tokenPath := filepath.Join(
		constants.GetPathConst().ConfPath,
		"rbd_mirror",
		fmt.Sprintf("%s_peer_keyring", cluster),
	)

	args := []string{
		"mirror", "pool", "peer", "bootstrap", "import", "--site-name", cluster, "--direction", "rx-tx", pool, tokenPath,
	}

	// add --cluster and --id args if remote op.
	if len(client) != 0 {
		args = appendRemoteClusterArgs(args, cluster, client)
	}

	_, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		return fmt.Errorf("failed to import peer bootstrap token: %v", err)
	}

	return nil
}

// peerRemove imports the bootstrap peer on the local cluster using a tokenfile.
func peerRemove(pool string, peerId string, localName string, remoteName string) error {
	args := []string{
		"mirror", "pool", "peer", "remove", pool, peerId,
	}

	// add --cluster and --id args
	args = appendRemoteClusterArgs(args, remoteName, localName)

	_, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		return fmt.Errorf("failed to remove peer(%s) for pool(%s): %v", peerId, pool, err)
	}

	return nil
}

// ########################### HELPERS ###########################

// appendRemoteClusterArgs appends the cluster and client arguments to ceph commands
func appendRemoteClusterArgs(args []string, cluster string, client string) []string {
	logger.Debugf("RBD Replication: old args are %v", args)
	// check if appendage is needed
	if len(cluster) == 0 && len(client) == 0 {
		// return as is
		return args
	}

	if len(cluster) > 0 {
		args = append(args, "--cluster")
		args = append(args, cluster)
	}

	if len(client) > 0 {
		args = append(args, "--id")
		args = append(args, client)
	}

	logger.Debugf("RBD Replication: new args are %v", args)

	// return modified args
	return args
}

// writeRemotePeerToken writes the provided string to a newly created token file.
func writeRemotePeerToken(token string, remoteName string) error {
	// create token Dir
	tokenDirPath := filepath.Join(
		constants.GetPathConst().ConfPath,
		"rbd_mirror",
	)

	err := os.MkdirAll(tokenDirPath, constants.PermissionWorldNoAccess)
	if err != nil {
		return fmt.Errorf("unable to create %q: %w", tokenDirPath, err)
	}

	// create token file
	tokenFilePath := filepath.Join(
		tokenDirPath,
		fmt.Sprintf("%s_peer_keyring", remoteName),
	)
	file, err := os.Create(tokenFilePath)
	if err != nil {
		ne := fmt.Errorf("failed to create the token file(%s): %w", tokenFilePath, err)
		logger.Error(ne.Error())
		return ne
	}

	// write to file
	_, err = file.WriteString(token)
	if err != nil {
		ne := fmt.Errorf("failed to write the token file(%s): %w", tokenFilePath, err)
		logger.Error(ne.Error())
		return ne
	}

	return nil
}
