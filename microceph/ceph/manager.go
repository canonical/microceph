package ceph

import (
	"fmt"
	"path/filepath"
)

func bootstrapMgr(hostname string, path string) error {
	args := []string{
		"auth",
		"get-or-create",
		fmt.Sprintf("mgr.%s", hostname),
		"mon", "allow profile mgr",
		"osd", "allow *",
		"mds", "allow *",
		"-o", filepath.Join(path, "keyring"),
	}

	_, err := cephRun(args...)
	if err != nil {
		return err
	}

	return nil
}
