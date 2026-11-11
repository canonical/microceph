package ceph

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/canonical/microceph/microceph/common"
)

func useSMBNativeProcesses(t *testing.T) {
	t.Helper()
	old, oldCommand := common.ProcessExec, newSMBPrivateCommandFunc
	common.ProcessExec = common.RunnerImpl{}
	// Unit fixtures supply inert native executables. Exercise real bounded
	// process groups without modifying host mounts; test the production child
	// namespace constructor separately and the mechanical library in Python.
	newSMBPrivateCommandFunc = func(ctx context.Context, root string, argv []string) (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = time.Second
		return cmd, nil
	}
	t.Cleanup(func() { common.ProcessExec, newSMBPrivateCommandFunc = old, oldCommand })
}
