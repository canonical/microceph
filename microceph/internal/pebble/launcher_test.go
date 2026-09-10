package pebble_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var helper string
var buildError error

// TestMain builds the private executable once for black-box launcher tests.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "microceph-pebble-test-")
	if err != nil {
		panic(err)
	}
	helper = filepath.Join(dir, "microceph-pebble")
	cmd := exec.Command("go", "build", "-o", helper, "../../cmd/microceph-pebble")
	out, err := cmd.CombinedOutput()
	if err != nil {
		buildError = fmt.Errorf("launcher is not buildable: %w: %s", err, out)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type fixture struct {
	snap, data, common string
}

func writeFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	err := os.MkdirAll(filepath.Dir(path), 0700)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(path, []byte(contents), mode)
	if err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{filepath.Join(root, "snap", "42"), filepath.Join(root, "data"), filepath.Join(root, "common")}
	writeFile(t, filepath.Join(f.snap, "bin", "pebble"), `#!/bin/sh
printf '%s\n' "$PEBBLE" "$PEBBLE_SOCKET" "$PEBBLE_PERSIST" "$@"
`, 0755)
	return f
}

func (f fixture) command(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	if buildError != nil {
		t.Fatal(buildError)
	}
	cmd := exec.Command(helper, args...)
	cmd.Env = append(os.Environ(), "SNAP="+f.snap, "SNAP_DATA="+f.data, "SNAP_COMMON="+f.common)
	return cmd
}

// TestSingletonBootstrap verifies the layer and exec contract for each singleton.
func TestSingletonBootstrap(t *testing.T) {
	for _, app := range []string{"daemon", "mon", "mgr", "mds", "rgw", "nfs", "rbd-mirror", "cephfs-mirror"} {
		t.Run(app, func(t *testing.T) {
			f := newFixture(t)
			out, err := f.command(t, "run", app).CombinedOutput()
			if err != nil {
				t.Fatalf("bootstrap: %v: %s", err, out)
			}
			root := filepath.Join(f.data, "pebble", app)
			want := root + "\n" + filepath.Join(f.common, "run", "pebble", app, ".pebble.socket") + "\nnever\nrun\n--verbose\n"
			if string(out) != want {
				t.Fatalf("exec environment/arguments: got %q, want %q", out, want)
			}
			path := filepath.Join(root, "layers", "001-microceph-"+app+".yaml")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var layer struct {
				Services map[string]map[string]string `json:"services"`
			}
			err = json.Unmarshal(data, &layer)
			if err != nil {
				t.Fatal(err)
			}
			service := layer.Services[app]
			wrapper := app
			if app == "nfs" {
				wrapper = "nfs-ganesha"
			}
			if len(layer.Services) != 1 || service["command"] != fmt.Sprintf("%q", filepath.Join(f.snap, "commands", wrapper+".start")) || service["startup"] != "enabled" || service["on-failure"] != "restart" || service["on-success"] != "ignore" {
				t.Fatalf("unexpected layer: %s", data)
			}
			for path, mode := range map[string]os.FileMode{root: 0700, filepath.Dir(path): 0700, path: 0600, filepath.Join(f.common, "run", "pebble", app): 0700} {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != mode {
					t.Fatalf("unsafe permissions on %s: info=%v error=%v", path, info, err)
				}
			}
			out, err = f.command(t, "run", app).CombinedOutput()
			if err != nil {
				t.Fatalf("repeat bootstrap: %v: %s", err, out)
			}
			repeated, err := os.ReadFile(path)
			if err != nil || string(repeated) != string(data) {
				t.Fatalf("layer is not deterministic: %v", err)
			}
		})
	}
}

// TestBootstrapRejectsNonDaemonApps keeps CLI apps and log rotation outside Pebble.
func TestBootstrapRejectsNonDaemonApps(t *testing.T) {
	for _, app := range []string{"log-rotate", "ceph", "../osd", "unknown"} {
		f := newFixture(t)
		out, err := f.command(t, "run", app).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "unsupported Pebble app") {
			t.Fatalf("%q was not rejected safely: %v: %s", app, err, out)
		}
	}
}

// TestOSDBootstrapOnlyIncludesEligibleIDs filters invalid and fenced OSDs.
func TestOSDBootstrapOnlyIncludesEligibleIDs(t *testing.T) {
	f := newFixture(t)
	for _, entry := range []string{"ceph-1/ready", "ceph-20/ready", "ceph-2/ready", "ceph-2/ready.removing", "ceph-3/fsid", "ceph-04/ready", "ceph-invalid/ready"} {
		writeFile(t, filepath.Join(f.common, "data", "osd", entry), "", 0600)
	}
	out, err := f.command(t, "run", "osd").CombinedOutput()
	if err != nil {
		t.Fatalf("OSD bootstrap: %v: %s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(f.data, "pebble", "osd", "layers", "001-microceph-osd.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var layer struct {
		Services map[string]map[string]string `json:"services"`
	}
	err = json.Unmarshal(data, &layer)
	if err != nil {
		t.Fatal(err)
	}
	if len(layer.Services) != 2 || layer.Services["osd-1"]["kill-delay"] != "5m" || layer.Services["osd-20"]["command"] != fmt.Sprintf("%q osd-run 20", filepath.Join(f.snap, "bin", "microceph-pebble")) {
		t.Fatalf("incorrect OSD inventory: %s", data)
	}
}
