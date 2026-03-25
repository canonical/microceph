package ceph

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/logger"
	"github.com/tidwall/gjson"
)

// Manager Daemon Ops
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

func getActiveMgrs() ([]string, error) {
	output, err := common.ProcessExec.RunCommand("ceph", "mgr", "dump", "-f", "json")
	if err != nil {
		logger.Errorf("Failed fetching Mgr dump: %v", err)
		return nil, err
	}

	logger.Debugf("Mgr Dump:\n%s", output)

	// Get the active mgr services.
	activeMgrs := []string{}
	result := gjson.Get(output, "standbys.#.name")
	for _, name := range result.Array() {
		activeMgrs = append(activeMgrs, name.String())
	}
	activeMgrs = append(activeMgrs, gjson.Get(output, "active_name").String())

	return activeMgrs, nil
}

// Mgr Module Ops

// EnableMgrModule enabled a mgr module on specified ceph cluster and verifies if is comes up
func EnableMgrModule(ctx context.Context, module string, remote string, local string) error {
	args := []string{"mgr", "module", "enable", module}

	cmd := appendRemoteClusterArgs(args, remote, local)

	_, err := cephRun(cmd...)
	if err != nil {
		logger.Errorf("Failed to enable remote cluster (%s) mgr module %s: %v", remote, module, err)
		return err
	}

	return nil
}

