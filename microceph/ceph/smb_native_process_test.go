package ceph

import (
	"testing"

	"github.com/canonical/microceph/microceph/common"
)

func useSMBNativeProcesses(t *testing.T) {
	t.Helper()
	old := common.ProcessExec
	common.ProcessExec = common.RunnerImpl{}
	t.Cleanup(func() { common.ProcessExec = old })
}
