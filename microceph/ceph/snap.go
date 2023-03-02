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

// snapReload restarts a service via snapctl.
func snapReload(service string) error {
	args := []string{
		"restart",
		"--reload",
		fmt.Sprintf("microceph.%s", service),
	}

	_, err := processExec.RunCommand("snapctl", args...)
	if err != nil {
		return err
	}

	return nil
}
