package ceph

import (
	"fmt"
	"path/filepath"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/common"
	"github.com/tidwall/gjson"
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

func getActiveMdss() ([]string, error) {
	output, err := common.ProcessExec.RunCommand("ceph", "fs", "status", "-f", "json")
	if err != nil {
		logger.Errorf("Failed fetching fs status: %v", err)
		return nil, err
	}

	logger.Debugf("Fs Status:\n%s", output)

	// Get the active mds services.
	activeMdss := []string{}
	result := gjson.Get(output, "mdsmap.#.name")
	for _, name := range result.Array() {
		activeMdss = append(activeMdss, name.String())
	}

	return activeMdss, nil
}
