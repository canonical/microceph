package ceph

import (
	"fmt"
)

// snapStart starts a service via snapctl, optionally enabling it.
func snapStart(service string, enable bool) error {
	args := []string{
		"start",
		fmt.Sprintf("microceph.%s", service),
	}

	if enable {
		args = append(args, "--enable")
	}

	_, err := processExec.RunCommand("snapctl", args...)
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

	_, err := processExec.RunCommand("snapctl", args...)
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

	_, err := processExec.RunCommand("snapctl", args...)
	if err != nil {
		return err
	}

	return nil
}
