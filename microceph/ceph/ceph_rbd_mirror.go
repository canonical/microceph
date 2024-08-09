package ceph

import (
	"fmt"
	"path/filepath"
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
		return err
	}

	return nil
}
