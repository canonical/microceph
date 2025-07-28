package ceph

import (
	"encoding/json"
	"fmt"
	"github.com/canonical/microceph/microceph/common"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Rican7/retry"
	"github.com/Rican7/retry/backoff"
	"github.com/Rican7/retry/strategy"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/tidwall/gjson"
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

	output, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Warnf("REPRBD: failed pool info operation on res(%s): %v", pool, err)
		return RbdReplicationPoolInfo{Mode: types.RbdResourceDisabled}, nil
	}

	err = json.Unmarshal([]byte(output), &response)
	if err != nil {
		ne := fmt.Errorf("cannot unmarshal rbd response: %v", err)
		logger.Errorf("REPRBD: %s", ne.Error())
		return RbdReplicationPoolInfo{Mode: types.RbdResourceDisabled}, ne
	}

	logger.Debugf("REPRBD: Pool Info: %v", response)

	return response, nil
}

// populatePoolStatus unmarshals the pool info json response into a structure.
func populatePoolStatus(status string) (RbdReplicationPoolStatus, error) {
	summary := RbdReplicationPoolStatusCmdOutput{}

	err := json.Unmarshal([]byte(status), &summary)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
		return RbdReplicationPoolStatus{}, err
	}

	return summary.Summary, nil
}

// GetRbdMirrorPoolStatus fetches mirroring status for requested pool
func GetRbdMirrorPoolStatus(pool string, cluster string, client string) (RbdReplicationPoolStatus, error) {
	args := []string{"mirror", "pool", "status", pool, "--format", "json"}

	output, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Warnf("failed pool status operation on res(%s): %v", pool, err)
		return RbdReplicationPoolStatus{State: StateDisabledReplication}, nil
	}

	logger.Infof("REPRBD: Raw Pool Status Output: %s", output)

	response, err := populatePoolStatus(output)
	if err != nil {
		ne := fmt.Errorf("cannot unmarshal rbd response: %v", err)
		logger.Errorf(ne.Error())
		return RbdReplicationPoolStatus{State: StateDisabledReplication}, ne
	}

	logger.Debugf("REPRBD: Pool Status: %v", response)

	// Count Images
	count := 0
	for _, v := range response.Description {
		count += v
	}

	// Patch required values
	response.State = StateEnabledReplication
	response.ImageCount = count

	return response, nil
}

// GetRbdMirrorVerbosePoolStatus fetches mirroring status for requested pool
func GetRbdMirrorVerbosePoolStatus(pool string, cluster string, client string) (RbdReplicationVerbosePoolStatus, error) {
	response := RbdReplicationVerbosePoolStatus{Name: pool}
	args := []string{"mirror", "pool", "status", pool, "--verbose", "--format", "json"}

	// Get verbose pool status
	output, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Warnf("REPRBD: failed verbose pool status operation on res(%s): %v", pool, err)
		return RbdReplicationVerbosePoolStatus{Summary: RbdReplicationPoolStatus{State: StateDisabledReplication}}, nil
	}

	logger.Debugf("REPRBD: Raw Pool Verbose Status: %s", string(output))

	// Unmarshal Summary into the structure.
	summary := gjson.Get(string(output), "summary")
	err = json.Unmarshal([]byte(summary.String()), &response.Summary)
	if err != nil {
		ne := fmt.Errorf("cannot unmarshal rbd response: %v", err)
		logger.Errorf(ne.Error())
		return RbdReplicationVerbosePoolStatus{Summary: RbdReplicationPoolStatus{State: StateDisabledReplication}}, ne
	}

	images := gjson.Get(string(output), "images")
	response.Images = make([]RbdReplicationImageStatus, len(images.Array()))

	// populate images
	for index, image := range images.Array() {
		err := json.Unmarshal([]byte(image.String()), &response.Images[index])
		if err != nil {
			name := gjson.Get(image.String(), "name")
			logger.Warnf("failed to parse the image data for (%s/%s)", pool, name)
		}

		response.Images[index].State = StateEnabledReplication
		response.Images[index].IsPrimary = strings.Contains(response.Images[index].Description, "local image is primary")
	}

	logger.Debugf("REPRBD: Pool Verbose Status: %v", response)

	// Patch required values
	response.Summary.State = StateEnabledReplication
	response.Summary.ImageCount = len(response.Images)

	return response, nil
}

// GetRbdMirrorImageStatus fetches mirroring status for reqeusted image
func GetRbdMirrorImageStatus(pool string, image string, cluster string, client string) (RbdReplicationImageStatus, error) {
	resource := fmt.Sprintf("%s/%s", pool, image)
	response := RbdReplicationImageStatus{}
	args := []string{"mirror", "image", "status", resource, "--format", "json"}

	output, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Warnf("failed image status operation on res(%s): %v", resource, err)
		return RbdReplicationImageStatus{State: StateDisabledReplication}, nil
	}

	err = json.Unmarshal([]byte(output), &response)
	if err != nil {
		ne := fmt.Errorf("cannot unmarshal rbd response: %v", err)
		logger.Errorf(ne.Error())
		return RbdReplicationImageStatus{State: StateDisabledReplication}, ne
	}

	logger.Debugf("REPRBD: Image Status: %v", response)

	// Patch required values
	response.State = StateEnabledReplication
	response.IsPrimary = strings.Contains(response.Description, "local image is primary")

	return response, nil
}

// EnablePoolMirroring enables mirroring for an rbd pool in pool mirroring or image mirroring mode.
func EnablePoolMirroring(pool string, mode types.RbdResourceType, localName string, remoteName string) error {
	// Enable pool mirroring on the local cluster.
	err := configurePoolMirroring(pool, mode, "", "")
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
		return err
	}

	// Enable pool mirroring on the remote cluster.
	err = configurePoolMirroring(pool, mode, localName, remoteName)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
		return err
	}

	// bootstrap peer
	return BootstrapPeer(pool, localName, remoteName)
}

// DisablePoolMirroring disables mirroring for an rbd pool.
func DisablePoolMirroring(pool string, peer RbdReplicationPeer, localName string, remoteName string) error {
	// remove peer permissions
	err := RemovePeer(pool, localName, remoteName)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
		return err
	}

	// Disable pool mirroring on the local cluster.
	err = configurePoolMirroring(pool, types.RbdResourceDisabled, "", "")
	if err != nil {
		logger.Errorf("REPRBD: failed to disable the primary pool mirroring %s", err.Error())
		return err
	}

	err = retry.Retry(func(i uint) error {
		// Disable pool mirroring on the remote cluster.
		err = configurePoolMirroring(pool, types.RbdResourceDisabled, localName, remoteName)
		if err != nil {
			logger.Errorf("REPRBD: attempt %d: %s", i, err.Error())
			return err
		}
		return nil
	}, strategy.Delay(5), strategy.Limit(10), strategy.Backoff(backoff.Linear(5*time.Second)))
	if err != nil {
		logger.Errorf("REPRBD: failed to disable the secondary pool mirroring: %s", err.Error())
		return err
	}

	return nil
}

// DisableAllMirroringImagesInPool disables mirroring for all images for a pool enabled in pool mirroring mode.
func DisableAllMirroringImagesInPool(poolName string) error {
	poolStatus, err := GetRbdMirrorVerbosePoolStatus(poolName, "", "")
	if err != nil {
		err := fmt.Errorf("failed to fetch status for %s pool: %v", poolName, err)
		logger.Errorf("REPRBD: %s", err.Error())
		return err
	}

	disabledImages := []string{}
	for _, image := range poolStatus.Images {
		err := disableRbdImageFeatures(poolName, image.Name, []string{"journaling"})
		if err != nil {
			return fmt.Errorf("failed to disable journaling on %s/%s", poolName, image.Name)
		}
		disabledImages = append(disabledImages, image.Name)
	}

	logger.Infof("REPRBD: Disabled %v images in %s pool.", disabledImages, poolName)
	return nil
}

// ResyncAllMirroringImagesInPool triggers a resync for all mirroring images inside a mirroring pool.
func ResyncAllMirroringImagesInPool(poolName string) error {
	poolStatus, err := GetRbdMirrorVerbosePoolStatus(poolName, "", "")
	if err != nil {
		err := fmt.Errorf("failed to fetch status for %s pool: %v", poolName, err)
		logger.Error(err.Error())
		return err
	}

	flaggedImages := []string{}
	for _, image := range poolStatus.Images {
		err := flagImageForResync(poolName, image.Name)
		if err != nil {
			return fmt.Errorf("failed to resync %s/%s", poolName, image.Name)
		}
		flaggedImages = append(flaggedImages, image.Name)
	}

	logger.Debugf("REPRBD: Resynced %v images in %s pool.", flaggedImages, poolName)
	return nil
}

// getPeerUUID returns the peer ID for the requested peer name.
func getPeerUUID(pool string, peerName string, client string, cluster string) (string, error) {
	poolInfo, err := GetRbdMirrorPoolInfo(pool, cluster, client)
	if err != nil {
		logger.Error(err.Error())
		return "", err
	}

	for _, peer := range poolInfo.Peers {
		if peer.RemoteName == peerName {
			return peer.Id, nil
		}
	}

	return "", fmt.Errorf("no peer found")
}

// RemovePeer removes the rbd-mirror peer permissions for requested pool.
func RemovePeer(pool string, localName string, remoteName string) error {
	// find local site's peer with name $remoteName
	localPeer, err := getPeerUUID(pool, remoteName, "", "")
	if err != nil {
		return err
	}

	remotePeer, err := getPeerUUID(pool, localName, localName, remoteName)
	if err != nil {
		return err
	}

	// Remove local cluster's peer
	err = peerRemove(pool, localPeer, "", "")
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
		return err
	}

	// Remove remote's peer
	err = peerRemove(pool, remotePeer, localName, remoteName)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
		return err
	}

	return nil
}

// BootstrapPeer bootstraps the rbd-mirror peer permissions for requested pool.
func BootstrapPeer(pool string, localName string, remoteName string) error {
	// create bootstrap token on local site.
	token, err := peerBootstrapCreate(pool, "", localName)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
		return err
	}

	// persist the peer token
	err = writeRemotePeerToken(token, remoteName)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
		return err
	}

	// import peer token on remote site
	return peerBootstrapImport(pool, localName, remoteName)
}

// ############################# Ceph Commands #############################
// configurePoolMirroring enables/disables mirroring for a pool.
func configurePoolMirroring(pool string, mode types.RbdResourceType, localName string, remoteName string) error {
	var args []string
	if mode == types.RbdResourceDisabled {
		args = []string{"mirror", "pool", "disable", pool}
	} else {
		args = []string{"mirror", "pool", "enable", pool, string(mode)}
	}

	// add --cluster and --id args
	args = appendRemoteClusterArgs(args, remoteName, localName)

	_, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
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

	_, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
		return fmt.Errorf("failed to configure rbd image feature: %v", err)
	}

	if mode == types.RbdReplicationSnapshot {
		err = createSnapshot(pool, image)
		if err != nil {
			logger.Errorf("REPRBD: %s", err.Error())
			return fmt.Errorf("failed to create image(%s/%s) snapshot : %v", pool, image, err)
		}

		err = configureSnapshotSchedule(pool, image, schedule, "")
		if err != nil {
			logger.Errorf("REPRBD: %s", err.Error())
			return fmt.Errorf("failed to create image(%s/%s) snapshot schedule(%s) : %v", pool, image, schedule, err)
		}
	}

	return nil
}

// getSnapshotSchedule fetches the schedule of the snapshots.
func getSnapshotSchedule(pool string, image string) (imageSnapshotSchedule, error) {
	if len(pool) == 0 || len(image) == 0 {
		return imageSnapshotSchedule{}, fmt.Errorf("ImageName(%s/%s) not complete", pool, image)
	}

	output, err := listSnapshotSchedule(pool, image)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
		return imageSnapshotSchedule{}, err
	}

	ret := []imageSnapshotSchedule{}
	err = json.Unmarshal(output, &ret)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
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

	output, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
		return []byte(""), err
	}

	return []byte(output), nil
}

func listAllImagesInPool(pool string, localName string, remoteName string) []string {
	args := []string{"ls", pool, "--format", "json"}

	// add --cluster and --id args
	args = appendRemoteClusterArgs(args, remoteName, localName)

	output, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		return []string{}
	}

	var ret []string
	err = json.Unmarshal([]byte(output), &ret)
	if err != nil {
		logger.Errorf("REPRBD: unexpected error encountered while parsing json output %s", output)
		return []string{}
	}

	return ret
}

func configureSnapshotSchedule(pool string, image string, schedule string, startTime string) error {
	var args []string
	if len(schedule) == 0 {
		logger.Debugf("Empty schedule, no-op for (%s/%s)", pool, image)
		return nil
	}

	args = []string{"mirror", "snapshot", "schedule", "add", "--pool", pool}

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

	_, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
		return err
	}

	return nil
}

// createSnapshot creates a snapshot of the requested image
func createSnapshot(pool string, image string) error {
	args := []string{"mirror", "image", "snapshot", fmt.Sprintf("%s/%s", pool, image)}

	_, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
		return err
	}

	return nil
}

// configureImageFeatures disables or enables requested feature on rbd image.
func configureImageFeatures(pool string, image string, op string, feature string) error {
	// op is enable or disable
	args := []string{"feature", op, fmt.Sprintf("%s/%s", pool, image), feature}

	_, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
		return fmt.Errorf("failed to configure rbd image feature: %v", err)
	}

	return nil
}

// enableImageFeatures enables the list of rbd features on the requested resource.
func enableRbdImageFeatures(poolName string, imageName string, features []string) error {
	for _, feature := range features {
		err := configureImageFeatures(poolName, imageName, "enable", feature)
		if err != nil && !strings.Contains(err.Error(), "one or more requested features are already enabled") {
			return err
		}
	}
	return nil
}

// disableRbdImageFeatures disables the list of rbd features on the requested resource.
func disableRbdImageFeatures(poolName string, imageName string, features []string) error {
	for _, feature := range features {
		err := configureImageFeatures(poolName, imageName, "disable", feature)
		if err != nil {
			return err
		}
	}
	return nil
}

// flagImageForResync flags requested mirroring image in the given pool for resync.
func flagImageForResync(poolName string, imageName string) error {
	args := []string{
		"mirror", "image", "resync", fmt.Sprintf("%s/%s", poolName, imageName),
	}

	_, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		return err
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

	output, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
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

	_, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
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

	_, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		logger.Errorf("REPRBD: %s", err.Error())
		return fmt.Errorf("failed to remove peer(%s) for pool(%s): %v", peerId, pool, err)
	}

	return nil
}

func promotePool(poolName string, isForce bool, remoteName string, localName string) error {
	args := []string{
		"mirror", "pool", "promote", poolName,
	}

	if isForce {
		args = append(args, "--force")
	}

	// add --cluster and --id args
	args = appendRemoteClusterArgs(args, remoteName, localName)

	output, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		return fmt.Errorf("failed to promote pool(%s): %v", poolName, err)
	}

	logger.Debugf("REPRBD: Promotion Output: %s", output)
	return nil
}

func demotePool(poolName string, remoteName string, localName string) error {
	args := []string{
		"mirror", "pool", "demote", poolName,
	}

	// add --cluster and --id args
	args = appendRemoteClusterArgs(args, remoteName, localName)

	output, err := common.ProcessExec.RunCommand("rbd", args...)
	if err != nil {
		return fmt.Errorf("failed to promote pool(%s): %v", poolName, err)
	}

	logger.Debugf("REPRBD: Demotion Output: %s", output)
	return nil
}

// ########################### HELPERS ###########################

func IsRemoteConfiguredForRbdMirror(remoteName string) bool {
	pools := ListPools("rbd")
	for _, pool := range pools {
		poolInfo, err := GetRbdMirrorPoolInfo(pool.Name, "", "")
		if err != nil {
			return false
		}

		for _, peer := range poolInfo.Peers {
			if peer.RemoteName == remoteName {
				return true
			}
		}
	}

	return false
}

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
		logger.Errorf("REPRBD: %s", err.Error())
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
		logger.Errorf("REPRBD: %s", ne.Error())
		return ne
	}

	// write to file
	_, err = file.WriteString(token)
	if err != nil {
		ne := fmt.Errorf("failed to write the token file(%s): %w", tokenFilePath, err)
		logger.Errorf("REPRBD: %s", ne.Error())
		return ne
	}

	return nil
}
