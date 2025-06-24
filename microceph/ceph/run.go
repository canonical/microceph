package ceph

func cephRun(args ...string) (string, error) {
	return processExec.RunCommand("ceph", args...)
}

func radosRun(args ...string) (string, error) {
	return processExec.RunCommand("rados", args...)
}

func ganeshaRadosGrace(cephConfPath, pool, namespace, userID, node string) (string, error) {
	return processExec.RunCommand("ganesha-rados-grace", "--cephconf", cephConfPath, "--pool", pool, "--ns", namespace, "--userid", userID, "add", node)
}
