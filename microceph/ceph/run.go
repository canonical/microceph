package ceph

func cephRun(args ...string) (string, error) {
	return processExec.RunCommand("ceph", args...)
}

func radosRun(args ...string) (string, error) {
	return processExec.RunCommand("rados", args...)
}
