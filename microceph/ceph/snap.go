package ceph

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/logger"
)

// Check if a snapd interface is connected to microceph
func isIntfConnected(name string) bool {
	args := []string{
		"is-connected",
		name,
	}

	_, err := common.ProcessExec.RunCommand("snapctl", args...)
	if err != nil { // Non-zero return code when connection not present.
		logger.Errorf("Failure: check is-connected %s: %v", name, err)
		return false
	}

	// 0 return code when connection is present
	return true
}

// snapStart starts a service via snapctl, optionally enabling it.
func snapStart(service string, enable bool) error {
	args := []string{
		"start",
		fmt.Sprintf("microceph.%s", service),
	}

	if enable {
		args = append(args, "--enable")
	}

	_, err := common.ProcessExec.RunCommand("snapctl", args...)
	if err != nil {
		return err
	}

	return nil
}

// snapStop stops a service via snapctl, optionally disabling it.
func snapStop(service string, disable bool) error {
	args := []string{
		"stop",
		fmt.Sprintf("microceph.%s", service),
	}

	if disable {
		args = append(args, "--disable")
	}

	_, err := common.ProcessExec.RunCommand("snapctl", args...)
	if err != nil {
		return err
	}

	return nil
}

// Restarts (optionally reloads) a service via snapctl.
func snapRestart(service string, isReload bool) error {
	args := []string{
		"restart",
	}

	if isReload {
		args = append(args, "--reload")
	}

	args = append(args, fmt.Sprintf("microceph.%s", service))

	_, err := common.ProcessExec.RunCommand("snapctl", args...)
	if err != nil {
		return err
	}

	return nil
}

// Check if a particular snap service is active or inactive
func snapCheckActive(service string) error {
	args := []string{
		"services",
		fmt.Sprintf("microceph.%s", service),
	}

	out, err := common.ProcessExec.RunCommand("snapctl", args...)
	if err != nil {
		return err
	}

	// Preserve the outer-app check, including the OSD aggregate used at startup.
	if strings.Contains(out, "inactive") {
		return fmt.Errorf("%s service is not active", service)
	}
	if service == "osd" {
		return nil
	}

	// A live supervisor may contain a failed child. Ceph-specific readiness
	// checks remain with the placement implementations.
	return pebbleCommand(common.ProcessExec, "status", service)
}

func pebbleCommand(runner common.Runner, operation, target string) error {
	binary := filepath.Join(os.Getenv("SNAP"), "bin", "microceph-pebble")
	_, err := runner.RunCommand(binary, operation, target)
	return err
}
