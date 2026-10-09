package pebble_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBootstrapReplacesInheritedPebbleEnvironment rejects duplicate routing
// variables: Go's environment lookup can select the first duplicate, not last.
func TestBootstrapReplacesInheritedPebbleEnvironment(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.snap, "bin", "pebble"), `#!/bin/sh
tr '\000' '\n' </proc/$$/environ | grep '^PEBBLE'
`, 0755)
	cmd := f.command(t, "run", "mon")
	cmd.Env = append(cmd.Env, "PEBBLE=/wrong", "PEBBLE_SOCKET=/wrong/socket", "PEBBLE_PERSIST=always")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"PEBBLE=", "PEBBLE_SOCKET=", "PEBBLE_PERSIST="} {
		count := 0
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, key) {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("expected one %s assignment, got %d", key, count)
		}
	}
	if strings.Contains(string(out), "/wrong") || strings.Contains(string(out), "=always") {
		t.Fatal("inherited Pebble routing was not replaced")
	}
}

// TestFailedLayerWriteKeepsPreviousLayer verifies atomic write failure behavior.
func TestFailedLayerWriteKeepsPreviousLayer(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.data, "pebble", "mon", "layers", "001-microceph-mon.yaml")
	writeFile(t, path, "previous layer\n", 0600)
	err := os.Mkdir(path+".tmp", 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.command(t, "run", "mon").CombinedOutput()
	if err == nil {
		t.Fatal("bootstrap accepted a failed layer write")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "previous layer\n" {
		t.Fatalf("previous layer was damaged: %v", err)
	}
}

// TestReloadContinuesAfterOneFailedStart keeps later OSDs from being stranded.
func TestReloadContinuesAfterOneFailedStart(t *testing.T) {
	f := newFixture(t)
	fakeControl(t, f, `{"services":{}}`)
	for _, id := range []string{"1", "2"} {
		writeFile(t, filepath.Join(f.common, "data", "osd", "ceph-"+id, "ready"), "", 0600)
	}
	path := filepath.Join(f.snap, "bin", "pebble")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "stop|start)", "start) printf '%s\\n' \"$*\" >> \"$SNAP_DATA/actions\"; [ \"$2\" != osd-1 ] ;;\nstop)", 1))
	writeFile(t, path, string(data), 0755)
	out, err := f.command(t, "reload", "osd").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "osd-1") {
		t.Fatalf("failed start not reported: %v: %s", err, out)
	}
	actions, err := os.ReadFile(filepath.Join(f.data, "actions"))
	if err != nil || string(actions) != "start osd-1\nstart osd-2\n" {
		t.Fatalf("later OSD stranded: %v: %s", err, actions)
	}
}
