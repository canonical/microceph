package ceph

import (
	"fmt"
	"path/filepath"

	"github.com/canonical/microceph/microceph/logger"
)

func bootstrapRbdMirror(hostname string, path string) error {
	args := []string{
		"auth",
		"get-or-create",
		fmt.Sprintf("client.rbd-mirror.%s", hostname),
		"mon", "profile rbd-mirror",
		"osd", "profile rbd",
		"-o", filepath.Join(path, "keyring"),
	}

	_, err := cephRun(args...)
	if err != nil {
		logger.Errorf("failed to bootstrap rbd-mirror daemon: %s", err.Error())
		return err
	}

	return nil
}
