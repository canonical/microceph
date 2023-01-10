package ceph

import (
	"fmt"
)

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
