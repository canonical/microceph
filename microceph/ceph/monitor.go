package ceph

import (
	"fmt"
	"os"
	"path/filepath"
)

func genMonmap(path string, fsid string) error {
	args := []string{
		"--create",
		"--fsid", fsid,
		path,
	}

	_, err := processExec.RunCommand("monmaptool", args...)
	if err != nil {
		return err
	}

	return nil
}

func addMonmap(path string, name string, address string) error {
	args := []string{
		"--add",
		name,
		address,
		path,
	}

	_, err := processExec.RunCommand("monmaptool", args...)
	if err != nil {
		return err
	}

	return nil
}

func bootstrapMon(hostname string, path string, monmap string, keyring string) error {
	args := []string{
		"--mkfs",
		"-i", hostname,
		"--mon-data", path,
		"--monmap", monmap,
		"--keyring", keyring,
	}

	_, err := processExec.RunCommand("ceph-mon", args...)
	if err != nil {
		return err
	}

	return nil
}

func joinMon(hostname string, path string) error {
	tmpPath, err := os.MkdirTemp("", "")
	if err != nil {
		return fmt.Errorf("Unable to create temporary path: %w", err)
	}
	defer os.RemoveAll(tmpPath)

	monmap := filepath.Join(tmpPath, "mon.map")
	_, err = cephRun("mon", "getmap", "-o", monmap)
	if err != nil {
		return fmt.Errorf("Failed to retrieve monmap: %w", err)
	}

	keyring := filepath.Join(tmpPath, "mon.keyring")
	_, err = cephRun("auth", "get", "mon.", "-o", keyring)
	if err != nil {
		return fmt.Errorf("Failed to retrieve mon keyring: %w", err)
	}

	return bootstrapMon(hostname, path, monmap, keyring)
}
