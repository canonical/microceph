package ceph

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"gopkg.in/yaml.v2"
)

type RbdMirrorCommand string

const (
	RbdMirrorStatusCommand RbdMirrorCommand = "status"
	RbdMirrorInfoCommand   RbdMirrorCommand = "info"
	RbdMirrorEnableCommand RbdMirrorCommand = "enable"
)

// Ceph Commands

// TODO: Add docs
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

// TODO: Add docs
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

// TODO: Add docs
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

	if len(cluster) != 0 {
		args = append(args, "--cluster")
		args = append(args, cluster)
	}

	if len(client) != 0 {
		args = append(args, "--id")
		args = append(args, client)
	}

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

func EnablePoolMirroring(pool string, localName string, remoteName string) error {
	// Execute Enable command for pool
	err := enablePoolMirroring(pool, "", "")
	if err != nil {
		return err
	}

	err = enablePoolMirroring(pool, remoteName, localName)
	if err != nil {
		return err
	}

	return BootstrapPeer(pool, localName, remoteName)
}
func EnableImageMirroring(pool string, image string, mode types.RbdReplicationType, localName string, remoteName string) error {
	return enableImageMirroring(pool, image)
}

func BootstrapPeer(pool string, localName string, remoteName string) error {
	var tokenPath string
	argsLocal := []string{
		"mirror", "pool", "peer", "bootstrap", "create", "--site-name", localName, pool, ">", tokenPath,
	}
	argsRemote := []string{
		"mirror", "pool", "peer", "bootstrap", "import", "--site-name", remoteName, "--direction",
		"rx-tx", pool, tokenPath, "--cluster", remoteName, "--id", localName,
	}

	_, err := processExec.RunCommand("rbd", argsLocal...)
	if err != nil {
		return fmt.Errorf("failed to execute bootstrap create: %v", err)
	}

	_, err = processExec.RunCommand("rbd", argsRemote...)
	if err != nil {
		return fmt.Errorf("failed to execute bootstrap import: %v", err)
	}

	return nil
}

// TODO: support image mode
func enablePoolMirroring(pool string, localName string, remoteName string) error {
	// TODO: remove hardcoding
	args := []string{"mirror", "pool", "enable", pool, "pool"}

	if len(remoteName) != 0 {
		args = append(args, "--cluster")
		args = append(args, remoteName)
	}

	if len(localName) != 0 {
		args = append(args, "--id")
		args = append(args, localName)
	}

	_, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		return fmt.Errorf("failed to execute rbd command: %v", err)
	}

	return nil
}

// TODO: remove journaling hardcode
func enableImageMirroring(pool string, image string) error {
	args := []string{"feature", "enable", fmt.Sprintf("%s/%s", pool, image), "journaling"}

	_, err := processExec.RunCommand("rbd", args...)
	if err != nil {
		return fmt.Errorf("failed to execute rbd command: %v", err)
	}

	return nil
}
