package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/canonical/microceph/microceph/logger"
)

// Contains methods for interacting with the Ceph FS mirror snapshot funtionality

type CephFSSnapshotMirrorPeer struct {
	ClientName string `json:"client_name"`
	SiteName   string `json:"site_name"`
	Volume     string `json:"fs_name"`
}

func cephFSSnapshotMirrorEnableVolume(volume string) error {
	_, err := cephRun(cephFSSnapshotMirrorCmd([]string{
		"enable", volume,
	})...)

	return err
}

func cephFSSnapshotMirrorDisableVolume(volume string) error {
	_, err := cephRun(cephFSSnapshotMirrorCmd([]string{
		"disable", volume,
	})...)

	return err
}

func cephFSSnapshotMirrorAddPath(ctx context.Context, volume string, path string) error {
	_, err := cephRunContext(ctx, cephFSSnapshotMirrorCmd([]string{
		"add", volume, path,
	})...)

	return err
}

func cephFSSnapshotMirrorRemovePath(ctx context.Context, volume string, path string) error {
	_, err := cephRunContext(ctx, cephFSSnapshotMirrorCmd([]string{
		"remove", volume, path,
	})...)

	return err
}

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

func cephFSSnapshotMirrorPeerImport(volume string, token string) error {
	_, err := cephRun(cephFSSnapshotMirrorCmd([]string{
		"peer_bootstrap", "import", volume, token,
	})...)

	return err
}

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

	for _, peer := range peers {
		if peer.SiteName == remoteName {
			return true, nil
		}
	}

	return false, nil
}

func cephFSSnapshotMirrorList(ctx context.Context, volume string) (string, error) {
	return cephRunContext(ctx, cephFSSnapshotMirrorCmd([]string{
		"ls", volume, "--format=json",
	})...)
}

func cephFSSnapshotMirrorDaemonStatus(ctx context.Context) (string, error) {
	return cephRunContext(ctx, cephFSSnapshotMirrorCmd([]string{
		"daemon", "status", "--format=json",
	})...)
}

// Helper Methods

func cephFSSnapshotMirrorCmd(args []string) []string {
	cmd := []string{
		"fs", "snapshot", "mirror",
	}

	return append(cmd, args...)
}

func dropEscapedChars(input string, dropStr string) string {
	ret := fmt.Sprintf("%s", []byte(input))

	logger.Debugf("Drop char Input (%s)", ret)

	retval := strings.ReplaceAll(ret, dropStr, ``)

	logger.Debugf("Drop char Output (%s)", retval)
	return retval
}
