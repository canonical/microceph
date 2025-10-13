package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/canonical/microceph/microceph/logger"
)

// Contains methods for interacting with the Ceph FS mirror snapshot funtionality

// CephFSSnapshotMirrorPeer encapsulates information about a single CephFS snapshot mirror peer
type CephFSSnapshotMirrorPeer struct {
	ClientName string `json:"client_name"`
	SiteName   string `json:"site_name"`
	Volume     string `json:"fs_name"`
}

// cephFSSnapshotMirrorEnableVolume enables mirroring for a specific CephFS volume
func cephFSSnapshotMirrorEnableVolume(volume string) error {
	_, err := cephRun(cephFSSnapshotMirrorCmd([]string{
		"enable", volume,
	})...)

	return err
}

// cephFSSnapshotMirrorDisableVolume disables mirroring for a specific CephFS volume
func cephFSSnapshotMirrorDisableVolume(volume string) error {
	_, err := cephRun(cephFSSnapshotMirrorCmd([]string{
		"disable", volume,
	})...)

	return err
}

// cephFSSnapshotMirrorAddPath enables mirroring for a specific path in a CephFS volume
func cephFSSnapshotMirrorAddPath(ctx context.Context, volume string, path string) error {
	_, err := cephRunContext(ctx, cephFSSnapshotMirrorCmd([]string{
		"add", volume, path,
	})...)

	return err
}

// cephFSSnapshotMirrorRemovePath disables a path from being mirrored in a volume
func cephFSSnapshotMirrorRemovePath(ctx context.Context, volume string, path string) error {
	_, err := cephRunContext(ctx, cephFSSnapshotMirrorCmd([]string{
		"remove", volume, path,
	})...)

	return err
}

// cephFSSnapshotMirrorPeerCreate generates a bootstrap token for a cephfs mirroring peer
func cephFSSnapshotMirrorPeerCreate(volume string, remoteName string, localName string) (string, error) {
	output, err := cephRun(cephFSSnapshotMirrorCmd([]string{
		"peer_bootstrap", "create", volume,
		fmt.Sprintf("client.fsmir-%s-%s", volume, remoteName),
		localName,
		// operation on remote cluster
		"--cluster", remoteName,
		"--id", localName,
		"-f", "json",
	})...)
	if err != nil {
		logger.Errorf("failed to create CephFS snapshot mirror peer: %v", err)
		return "", err
	}

	logger.Debugf("CephFS snapshot mirror peer create output:(%s)", output)

	ret := strings.ReplaceAll(output, "\n", "")

	return ret, nil
}

// cephFSSnapshotMirrorPeerImport imports a cephfs mirroring peer using the provided token
func cephFSSnapshotMirrorPeerImport(volume string, token string) error {
	_, err := cephRun(cephFSSnapshotMirrorCmd([]string{
		"peer_bootstrap", "import", volume, token,
	})...)

	return err
}

// cephFSSnapshotMirrorPeerExists checks if a mirroring peer with the given remote name exists for the specified volume
func cephFSSnapshotMirrorPeerExists(ctx context.Context, volume string, remoteName string) (bool, error) {
	output, err := cephRunContext(ctx, cephFSSnapshotMirrorCmd([]string{
		"peer_list", volume, "--format=json",
	})...)
	if err != nil {
		err = fmt.Errorf("failed to get CephFS snapshot mirror list: %w", err)
		logger.Error(err.Error())
		return false, err
	}

	peers := map[string]CephFSSnapshotMirrorPeer{}
	err = json.Unmarshal([]byte(output), &peers)
	if err != nil {
		err = fmt.Errorf("failed to parse CephFS snapshot mirror list: %w", err)
		logger.Error(err.Error())
		return false, err
	}

	logger.Debugf("CephFS snapshot peers found: %+v", peers)

	for _, peer := range peers {
		if peer.SiteName == remoteName {
			return true, nil
		}
	}

	return false, nil
}

// cephFSSnapshotMirrorList fetches the list of paths enabled for mirroring in a volume
func cephFSSnapshotMirrorList(ctx context.Context, volume string) (string, error) {
	return cephRunContext(ctx, cephFSSnapshotMirrorCmd([]string{
		"ls", volume, "--format=json",
	})...)
}

// cephFSSnapshotMirrorDaemonStatus fetches the cephfs mirroring daemon status
func cephFSSnapshotMirrorDaemonStatus(ctx context.Context) (string, error) {
	return cephRunContext(ctx, cephFSSnapshotMirrorCmd([]string{
		"daemon", "status", "--format=json",
	})...)
}

// Helper Methods

// cephFSSnapshotMirrorCmd prefixes the snapshot mirror command
func cephFSSnapshotMirrorCmd(args []string) []string {
	cmd := []string{
		"fs", "snapshot", "mirror",
	}

	return append(cmd, args...)
}
