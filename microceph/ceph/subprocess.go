package ceph

import "github.com/lxc/lxd/shared"

// Runner launches processes
type Runner interface {
	RunCommand(name string, arg ...string) (string, error)
}

// RunnerImpl for launching processes
type RunnerImpl struct{}

// RunCommand runs a process given a path to a binary and a list of args
func (c RunnerImpl) RunCommand(name string, arg ...string) (string, error) {
	return shared.RunCommand(name, arg...)
}

// Singleton runner: make this patch-able for testing purposes.
// By default executes via shared.RunCommand()
var processExec Runner = RunnerImpl{}
