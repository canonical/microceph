package ceph

import (
	"fmt"
	"path/filepath"

	"github.com/canonical/lxd/shared/logger"
)

func bootstrapCephExporter(hostname string, path string) error {
	args := []string{
		"auth",
		"get-or-create",
		fmt.Sprintf("client.ceph-exporter.%s", hostname),
		"mon", "profile ceph-exporter",
		"-o", filepath.Join(path, "keyring"),
	}

	_, err := cephRun(args...)
	if err != nil {
		logger.Errorf("failed to bootstrap ceph-exporter daemon: %s", err.Error())
		return err
	}

	return nil
}
