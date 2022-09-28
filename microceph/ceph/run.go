package ceph

import (
	"github.com/lxc/lxd/shared"
)

func cephRun(args ...string) (string, error) {
	return shared.RunCommand("ceph", args...)
}
