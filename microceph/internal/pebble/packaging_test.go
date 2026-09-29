package pebble_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestSnapServiceBoundaries guards the all-nine-app cutover and permissions.
func TestSnapServiceBoundaries(t *testing.T) {
	data, err := os.ReadFile("../../../snap/snapcraft.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Apps map[string]struct {
			Command     string   `yaml:"command"`
			Daemon      string   `yaml:"daemon"`
			InstallMode string   `yaml:"install-mode"`
			StopMode    string   `yaml:"stop-mode"`
			StopTimeout string   `yaml:"stop-timeout"`
			After       []string `yaml:"after"`
			Plugs       []string `yaml:"plugs"`
			Slots       []string `yaml:"slots"`
		} `yaml:"apps"`
	}
	err = yaml.Unmarshal(data, &manifest)
	if err != nil {
		t.Fatal(err)
	}
	plugs := map[string][]string{
		"daemon":        {"block-devices", "dm-crypt", "hardware-observe", "mount-observe", "network", "network-bind", "microceph-support"},
		"mds":           {"network", "network-bind", "process-control"},
		"mon":           {"hardware-observe", "network", "network-bind", "process-control"},
		"mgr":           {"network", "network-bind", "process-control"},
		"nfs":           {"network", "network-bind", "process-control"},
		"osd":           {"block-devices", "dm-crypt", "hardware-observe", "network", "network-bind", "microceph-support", "process-control"},
		"rgw":           {"hardware-observe", "network", "network-bind", "process-control"},
		"rbd-mirror":    {"network", "network-bind", "process-control"},
		"cephfs-mirror": {"network", "network-bind", "process-control"},
	}
	for name, permissions := range plugs {
		t.Run(name, func(t *testing.T) {
			app := manifest.Apps[name]
			if app.Command != "bin/microceph-pebble run "+name || app.Daemon != "simple" {
				t.Errorf("not switched to private launcher: %+v", app)
			}
			if !reflect.DeepEqual(app.Plugs, permissions) {
				t.Errorf("confinement boundary changed: %v", app.Plugs)
			}
			if name != "daemon" && (app.InstallMode != "disable" || !reflect.DeepEqual(app.After, []string{"daemon"})) {
				t.Error("enablement or ordering changed")
			}
			if name == "daemon" && (app.InstallMode != "" || len(app.After) != 0 || !reflect.DeepEqual(app.Slots, []string{"microceph"})) {
				t.Error("daemon enablement, ordering, or slot changed")
			}
			timeout := "45s"
			if name == "osd" {
				timeout = "6m"
			}
			if app.StopTimeout != timeout || app.StopMode != "sigterm-all" {
				t.Errorf("missing child grace/cgroup headroom: %+v", app)
			}
		})
	}
	_, exposed := manifest.Apps["microceph-pebble"]
	if exposed {
		t.Error("adapter must not become a public Snap command")
	}
}

// TestPebblePackagingAndLogRotation guards the pin and direct oneshot exception.
func TestPebblePackagingAndLogRotation(t *testing.T) {
	data, err := os.ReadFile("../../../snap/snapcraft.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Apps  map[string]map[string]any `yaml:"apps"`
		Parts map[string]struct {
			Source string   `yaml:"source"`
			Commit string   `yaml:"source-commit"`
			Build  string   `yaml:"override-build"`
			Prime  []string `yaml:"prime"`
		} `yaml:"parts"`
	}
	err = yaml.Unmarshal(data, &manifest)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"command": "commands/log-rotate.start", "daemon": "oneshot", "timer": "00:02,06:04,12:07,18:03", "after": []any{"daemon"}}
	if !reflect.DeepEqual(manifest.Apps["log-rotate"], want) {
		t.Errorf("log rotation changed: %#v", manifest.Apps["log-rotate"])
	}
	part := manifest.Parts["pebble"]
	if part.Source != "https://github.com/canonical/pebble.git" || part.Commit != "e712a58489c17089c9bc89a3e26d9d4aee89e957" {
		t.Error("missing qualified Pebble source pin")
	}
	for _, flag := range []string{"go1.26.7", "CGO_ENABLED=0", "-mod=readonly", "-trimpath", "COPYING"} {
		if !strings.Contains(part.Build, flag) {
			t.Errorf("Pebble build missing %s", flag)
		}
	}
	microceph := manifest.Parts["microceph"]
	if !strings.Contains(microceph.Build, "./cmd/microceph-pebble") || !strings.Contains(strings.Join(microceph.Prime, " "), "bin/microceph-pebble") {
		t.Error("private adapter is not built and primed")
	}
}

// TestForegroundOSDWrapper verifies foreground exec and the removal fence with
// harmless binaries. Actual LUKS devices remain an integration-test requirement.
func TestForegroundOSDWrapper(t *testing.T) {
	wrapper, err := os.ReadFile("../../../snapcraft/commands/osd.run")
	if err != nil {
		t.Fatal(err)
	}
	for _, fenced := range []bool{false, true} {
		f := newFixture(t)
		writeFile(t, filepath.Join(f.data, "conf", "ceph.conf"), "run dir = test\n", 0600)
		writeFile(t, filepath.Join(f.common, "data", "osd", "ceph-7", "ready"), "", 0600)
		if fenced {
			writeFile(t, filepath.Join(f.common, "data", "osd", "ceph-7", "ready.removing"), "", 0600)
		}
		writeFile(t, filepath.Join(f.snap, "commands", "osd.run"), string(wrapper), 0755)
		writeFile(t, filepath.Join(f.snap, "commands", "common"), "limits() { :; }\nwait_for_config() { :; }\n", 0600)
		writeFile(t, filepath.Join(f.snap, "bin", "ceph-osd"), "#!/bin/sh\nprintf '%s\\n' \"$@\"\nexit 23\n", 0755)
		cmd := f.command(t, "unused", "unused")
		cmd.Path = filepath.Join(f.snap, "commands", "osd.run")
		cmd.Args = []string{cmd.Path, "7"}
		cmd.Env = append(cmd.Env, "PATH="+filepath.Join(f.snap, "bin")+":"+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		if fenced {
			if err != nil || len(out) != 0 {
				t.Fatalf("fenced OSD launched: %v: %s", err, out)
			}
		} else if cmd.ProcessState.ExitCode() != 23 || string(out) != "--foreground\n--cluster\nceph\n--id\n7\n" {
			t.Fatalf("foreground command or child exit changed: %v: %s", err, out)
		}
	}
}
