package ceph

import (
	"fmt"
	"path/filepath"

	"github.com/canonical/lxd/shared/logger"
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
