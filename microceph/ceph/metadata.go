package ceph

import (
	"fmt"
	"path/filepath"
)

func bootstrapMds(hostname string, path string) error {
	args := []string{
		"auth",
		"get-or-create",
		fmt.Sprintf("mds.%s", hostname),
		"mon", "allow profile mds",
		"mgr", "allow profile mds",
		"mds", "allow *",
		"osd", "allow *",
		"-o", filepath.Join(path, "keyring"),
	}

	_, err := cephRun(args...)
	if err != nil {
		return err
	}

	return nil
}
