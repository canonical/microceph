package ceph

import (
	"context"

	"github.com/canonical/microceph/microceph/common"
)

func cephRun(args ...string) (string, error) {
	return common.ProcessExec.RunCommand("ceph", args...)
}

func cephRunContext(ctx context.Context, args ...string) (string, error) {
	return common.ProcessExec.RunCommandContext(ctx, "ceph", args...)
}

func radosRun(args ...string) (string, error) {
	return common.ProcessExec.RunCommand("rados", args...)
}
