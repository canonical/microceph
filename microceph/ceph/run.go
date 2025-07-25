package ceph

import "github.com/canonical/microceph/microceph/common"

func cephRun(args ...string) (string, error) {
	return common.ProcessExec.RunCommand("ceph", args...)
}

func radosRun(args ...string) (string, error) {
	return common.ProcessExec.RunCommand("rados", args...)
}
