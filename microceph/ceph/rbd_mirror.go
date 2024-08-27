package ceph

import (
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

type RbdMirrorCommand string

const (
	RbdMirrorStatusCommand RbdMirrorCommand = "status"
	RbdMirrorInfoCommand   RbdMirrorCommand = "info"
	RbdMirrorEnableCommand RbdMirrorCommand = "enable"
)

// Ceph Commands

// GetRbdMirrorPoolInfo fetches the mirroring info for the requested pool
func GetRbdMirrorPoolInfo(pool string, cluster string, client string) (RbdReplicationPoolInfo, error) {
	respObj := executeMirrorCommand(pool, types.RbdResourcePool, RbdMirrorInfoCommand, cluster, client)
	if respObj == nil {
		return RbdReplicationPoolInfo{Mode: types.RbdResourceDisabled}, nil
	}

	logger.Infof("BAZINGA %v", respObj)

	mode := types.RbdResourceType(respObj["Mode"].(string))
	if mode == types.RbdResourceDisabled {
		logger.Debugf("RBD Replication: Pool(%s) state disabled.", pool)
		return RbdReplicationPoolInfo{Mode: types.RbdResourceDisabled}, nil
	}

	// generate poolInfo without peers
	poolInfo := RbdReplicationPoolInfo{Mode: mode, LocalSiteName: respObj["Site Name"].(string)}

	// populate peers if available
	if strings.Contains(respObj["Site Name"].(string), "none") {
		poolInfo.Peers = []RbdReplicationPeer{}
	} else {
		poolInfo.Peers = []RbdReplicationPeer{
			{
				LocalId:    respObj["UUID"].(string),
				RemoteId:   respObj["Mirror UUID"].(string),
				RemoteName: respObj["Name"].(string),
				Direction:  types.RbdReplicationDirection(respObj["Direction"].(string)),
			},
		}
	}

	return poolInfo, nil
}

// GetRbdMirrorPoolStatus fetches mirroring status for requested pool
func GetRbdMirrorPoolStatus(pool string, cluster string, client string) (RbdReplicationPoolStatus, error) {
	respObj := executeMirrorCommand(pool, types.RbdResourcePool, RbdMirrorStatusCommand, cluster, client)
	if respObj == nil {
		return RbdReplicationPoolStatus{State: StateDisabledReplication}, nil
	}

	logger.Infof("BAZINGA %v", respObj)

	imageCount, err := strconv.Atoi(strings.Split(respObj["images"].(string), " ")[0])
	if err != nil {
		return RbdReplicationPoolStatus{}, fmt.Errorf("failed to convert %s to int: %w", respObj["images"], err)
	}

	return RbdReplicationPoolStatus{
		State:        StateEnabledReplication,
		Health:       RbdReplicationHealth(respObj["health"].(string)),
		DaemonHealth: RbdReplicationHealth(respObj["daemon health"].(string)),
		ImageHealth:  RbdReplicationHealth(respObj["image health"].(string)),
		ImageCount:   imageCount,
	}, nil
}

// GetRbdMirrorImageStatus fetches mirroring status for reqeusted image
func GetRbdMirrorImageStatus(pool string, image string, cluster string, client string) (RbdReplicationImageStatus, error) {
	resourceName := fmt.Sprintf("%s/%s", pool, image)
	respObj := executeMirrorCommand(resourceName, types.RbdResourceImage, RbdMirrorStatusCommand, cluster, client)
	if respObj == nil {
		return RbdReplicationImageStatus{State: StateDisabledReplication}, nil
	}

	logger.Infof("BAZINGA %v", respObj)

	var imageStatus map[string]interface{}
	var peers map[string]interface{}

	err := yaml.Unmarshal([]byte(respObj[image].(string)), imageStatus)
	if err != nil {
		return RbdReplicationImageStatus{}, fmt.Errorf("cannot unmarshal image(%s) status: %v", image, err)
	}

	err = yaml.Unmarshal([]byte(imageStatus["peer_sites"].(string)), peers)
	if err != nil {
		return RbdReplicationImageStatus{}, fmt.Errorf("cannot unmarshal image(%s) peers: %v", image, err)
	}

	return RbdReplicationImageStatus{
		ID:         imageStatus["global_id"].(string),
		State:      StateEnabledReplication,
		Status:     imageStatus["state"].(string),
		LastUpdate: imageStatus["last_update"].(string),
		isPrimary:  strings.Contains(imageStatus["description"].(string), "local image is primary"),
		Peers: []string{
			peers["name"].(string),
		},
	}, nil
}

func executeMirrorCommand(resourceName string, resourceType types.RbdResourceType, command RbdMirrorCommand, cluster string, client string) map[string]interface{} {
	respObj := make(map[string]interface{})
	args := []string{"mirror", string(resourceType), string(command), resourceName}

	// add --cluster and --id args
	args = appendRemoteClusterArgs(args, cluster, client)

	output, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Warnf("failed %s operation on res(%s): %v", string(command), resourceName, err)
		return nil
	}

	err = yaml.Unmarshal([]byte(output), respObj)
	if err != nil {
		logger.Errorf("cannot unmarshal rbd response: %v", err)
		return nil
	}

	return respObj
}

func EnablePoolMirroring(pool string, mode types.RbdResourceType, localName string, remoteName string) error {
	// Enable pool on the local cluster.
	err := configurePoolMirroring(pool, mode, "", "")
	if err != nil {
		return err
	}

	// Enable pool mirroring on the remote cluster.
	err = configurePoolMirroring(pool, mode, remoteName, localName)
	if err != nil {
		return err
	}

	// Bootstrap peer token for mirroring.
	return BootstrapPeer(pool, localName, remoteName)
}
func EnableImageMirroring(pool string, image string, mode types.RbdReplicationType, localName string, remoteName string) error {
	return configureImageMirroring(pool, image, types.RbdReplicationSnapshot)
}

func BootstrapPeer(pool string, localName string, remoteName string) error {
	tokenPath := filepath.Join(
		constants.GetPathConst().ConfPath,
		"rbd_mirror",
		fmt.Sprintf("%s_peer_keyring", remoteName),
	)

	// create bootstrap token on remote site.
	token, err := peerBootstrapCreate(pool, remoteName, localName)
	if err != nil {
		logger.Error(err.Error())
		return err
	}

	// persist the peer token
	err = writeTokenToPath(token, tokenPath)
	if err != nil {
		logger.Error(err.Error())
		return err
	}

	// import peer token on local site
	return peerBootstrapImport(pool, tokenPath, remoteName, localName)
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

// configureImageMirroring disables or enabled image mirroring in requested mode.
func configureImageMirroring(pool string, image string, mode types.RbdReplicationType) error {
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

	return nil
}

// enableImageFeatures disables or enables requested feature on rbd image.
func enableImageFeatures(pool string, image string, op string, feature string) error {
	// op is enable or disable
	args := []string{"feature", op, fmt.Sprintf("%s/%s", pool, image), feature}

	_, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		return fmt.Errorf("failed to configure rbd image feature: %v", err)
	}

	return nil
}

// peerBootstrapCreate generates peer bootstrap token on remote ceph cluster.
func peerBootstrapCreate(pool string, remoteName string, localName string) (string, error) {
	args := []string{
		"mirror", "pool", "peer", "bootstrap", "create", "--site-name", localName, pool,
	}

	// add --cluster and --id args
	args = appendRemoteClusterArgs(args, remoteName, localName)

	output, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		return "", fmt.Errorf("failed to bootstrap peer token: %v", err)
	}

	return output, nil
}

// peerBootstrapImport imports the bootstrap peer on the local cluster using a tokenfile.
func peerBootstrapImport(pool string, tokenPath string, remoteName string, localName string) error {
	args := []string{
		"mirror", "pool", "peer", "bootstrap", "import", "--site-name", localName, "--direction", "rx-tx", pool, tokenPath,
	}

	// add --cluster and --id args
	args = appendRemoteClusterArgs(args, remoteName, localName)

	_, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		return fmt.Errorf("failed to import peer bootstrap token: %v", err)
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

// writeTokenToPath writes the provided string to a newly created token file.
func writeTokenToPath(token string, path string) error {
	// create file
	file, err := os.Create(path)
	if err != nil {
		ne := fmt.Errorf("failed to create the token file(%s): %w", path, err)
		logger.Error(ne.Error())
		return ne
	}

	// write to file
	_, err = file.WriteString(token)
	if err != nil {
		ne := fmt.Errorf("failed to write the token file(%s): %w", path, err)
		logger.Error(ne.Error())
		return ne
	}

	return nil
}
