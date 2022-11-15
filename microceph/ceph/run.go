package ceph

func cephRun(args ...string) (string, error) {
	return processExec.RunCommand("ceph", args...)
}
