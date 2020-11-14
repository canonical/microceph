package main

import (
	"io/ioutil"
	"os"
	"path"
	"testing"
)

func testCephConf(t *testing.T, tmpdir string) {
	conf := Conf{}
	var c CephConf = &conf
	var err error

	// Try to retrieve config without `ceph-conf` binary in path
	err = c.Load()
	if err == nil {
		t.Errorf("Load = nil; want error")
	}
	// Stage a fake `ceph-conf` binary in PATH and test
	scriptfile := writeFakeCephConfScript(tmpdir)
	err = c.Load()
	os.Remove(scriptfile)
	if err != nil {
		t.Errorf("Load = %s; want nil", err)
	}
	// Check that non-existent value is flagged
	v, ok := c.Get("non-existent")
	if ok != false {
		t.Errorf("Get = '%s', %t; want '', false", v, ok)
	}
	// Check that the expected value is there
	v, ok = c.Get("key")
	if v != "value" {
		t.Errorf("Load produced map %s; want [key: value]", conf.data)
	}
}

func TestCephConf(t *testing.T) {
	tmpdir, err := ioutil.TempDir("/tmp", "testcephconf")
	if err != nil {
		t.Errorf("Unable too create tmp directory: %s", err)
	}
	stored_path := os.Getenv("PATH")
	os.Setenv("PATH", tmpdir)

	testCephConf(t, tmpdir)

	os.Setenv("PATH", stored_path)
	os.Remove(tmpdir)
}

func writeFakeCephConfScript(dir string) string {
	file := path.Join(dir, "ceph-conf")
	script := []byte(ceph_conf_script)
	ioutil.WriteFile(file, script, 0755)
	return file
}

const (
	ceph_conf_script = `#!/bin/sh
echo '{"key":"value"}'
`
)
