package ceph

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"

	"github.com/tidwall/gjson"
)

func createMonMap(s interfaces.StateInterface, path string, fsid string, address string) error {
	// Generate initial monitor map.
	err := genMonmap(filepath.Join(path, "mon.map"), fsid)
	if err != nil {
		return fmt.Errorf("failed to generate monitor map: %w", err)
	}

	err = addMonmap(filepath.Join(path, "mon.map"), s.ClusterState().Name(), address)
	if err != nil {
		return fmt.Errorf("failed to add monitor map: %w", err)
	}

	return nil
}

func genMonmap(path string, fsid string) error {
	args := []string{
		"--create",
		"--fsid", fsid,
		path,
	}

	_, err := common.ProcessExec.RunCommand("monmaptool", args...)
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

	_, err := common.ProcessExec.RunCommand("monmaptool", args...)
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

	_, err := common.ProcessExec.RunCommand("ceph-mon", args...)
	if err != nil {
		return err
	}

	return nil
}

func joinMon(hostname string, path string) error {
	tmpPath, err := os.MkdirTemp("", "")
	if err != nil {
		return fmt.Errorf("unable to create temporary path: %w", err)
	}
	defer os.RemoveAll(tmpPath)

	monmap := filepath.Join(tmpPath, "mon.map")
	_, err = cephRun("mon", "getmap", "-o", monmap)
	if err != nil {
		return fmt.Errorf("failed to retrieve monmap: %w", err)
	}

	keyring := filepath.Join(tmpPath, "mon.keyring")
	_, err = cephRun("auth", "get", "mon.", "-o", keyring)
	if err != nil {
		return fmt.Errorf("failed to retrieve mon keyring: %w", err)
	}

	return bootstrapMon(hostname, path, monmap, keyring)
}

// removeMon removes a monitor from the cluster.
func removeMon(hostname string) error {
	_, err := cephRun("mon", "rm", hostname)
	if err != nil {
		logger.Errorf("failed to remove monitor %q: %v", hostname, err)
		return fmt.Errorf("failed to remove monitor %q: %w", hostname, err)
	}
	return nil
}

func getActiveMons() ([]string, error) {
	output, err := common.ProcessExec.RunCommand("ceph", "-s", "-f", "json")
	if err != nil {
		logger.Errorf("Failed fetching ceph status: %v", err)
		return nil, err
	}

	logger.Debugf("Ceph Status:\n%s", output)

	// Get the active mons services.
	activeMons := []string{}
	result := gjson.Get(output, "quorum_names")
	for _, name := range result.Array() {
		activeMons = append(activeMons, name.String())
	}

	return activeMons, nil
}
