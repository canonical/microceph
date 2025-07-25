package common

import (
	"context"
	"github.com/canonical/lxd/shared"
)

// Runner launches processes
type Runner interface {
	RunCommand(name string, arg ...string) (string, error)
	RunCommandContext(ctx context.Context, name string, arg ...string) (string, error)
}

// RunnerImpl for launching processes
type RunnerImpl struct{}

// RunCommand runs a process given a path to a binary and a list of args
func (c RunnerImpl) RunCommand(name string, arg ...string) (string, error) {
	return shared.RunCommand(name, arg...)
}

// RunCommandContext runs a process given a context, a path to a binary and a list of args
func (c RunnerImpl) RunCommandContext(ctx context.Context, name string, arg ...string) (string, error) {
	return shared.RunCommandContext(ctx, name, arg...)
}

// Singleton runner: make this patch-able for testing purposes.
// By default executes via shared.RunCommand()
var ProcessExec Runner = RunnerImpl{}
