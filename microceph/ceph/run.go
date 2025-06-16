package ceph

func cephRun(args ...string) (string, error) {
	return processExec.RunCommand("ceph", args...)
}

func osdEnablePool(pool, app string) (string, error) {
	return cephRun("osd", "pool", "application", "enable", pool, app)
}

func radosRun(args ...string) (string, error) {
	return processExec.RunCommand("rados", args...)
}

func radosCreatePool(pool string) (string, error) {
	return radosRun("ls", "--pool", pool, "--all", "--create")
}

func radosCreateObject(pool, namespace, object string) (string, error) {
	return radosRun("create", "-p", pool, "-N", namespace, object)
}

func ganeshaRadosGrace(cephConfPath, pool, namespace, userID, node string) (string, error) {
	return processExec.RunCommand("ganesha-rados-grace", "--cephconf", cephConfPath, "--pool", pool, "--ns", namespace, "--userid", userID, "add", node)
}
