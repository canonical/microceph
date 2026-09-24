package pebble_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func fakeControl(t *testing.T, f fixture, status string) {
	t.Helper()
	writeFile(t, filepath.Join(f.data, "status.json"), status, 0600)
	writeFile(t, filepath.Join(f.snap, "bin", "pebble"), `#!/bin/sh
set -eu
case "$1" in
services) cat "$SNAP_DATA/status.json" ;;
add) cp "$4" "$SNAP_DATA/added.json" ;;
stop|start) printf '%s\n' "$*" >> "$SNAP_DATA/actions" ;;
*) exit 42 ;;
esac
`, 0755)
}

// TestChildStatus rejects a failing child even when its supervisor is available.
func TestChildStatus(t *testing.T) {
	for _, state := range []string{"active", "backoff", "error", "inactive", "unknown"} {
		t.Run(state, func(t *testing.T) {
			f := newFixture(t)
			fakeControl(t, f, fmt.Sprintf(`{"services":{"mon":{"name":"mon","startup":"enabled","current":%q}}}`, state))
			out, err := f.command(t, "status", "mon").CombinedOutput()
			if state == "active" {
				if err != nil {
					t.Fatalf("active child rejected: %v: %s", err, out)
				}
			} else if err == nil || !strings.Contains(string(out), "not active") {
				t.Fatalf("%s child accepted or wrong failure: %v: %s", state, err, out)
			}
		})
	}
}

// TestChildStatusFailsClosed covers unavailable or invalid status information.
func TestChildStatusFailsClosed(t *testing.T) {
	for _, status := range []string{`{"services":{}}`, `not json`, `{"services":{"other":{"current":"active"}}}`} {
		f := newFixture(t)
		fakeControl(t, f, status)
		out, err := f.command(t, "status", "mon").CombinedOutput()
		if err == nil || strings.Contains(string(out), "usage:") {
			t.Fatalf("invalid status was not checked: %v: %s", err, out)
		}
	}
}

func osdChild(t *testing.T, f fixture, script string) *exec.Cmd {
	t.Helper()
	writeFile(t, filepath.Join(f.common, "data", "osd", "ceph-1", "ready"), "", 0600)
	writeFile(t, filepath.Join(f.snap, "commands", "osd.run"), "#!/bin/sh\n"+script, 0755)
	cmd := f.command(t, "osd-run", "1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err := cmd.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// The test may already have killed and reaped the child.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	receipt := filepath.Join(f.common, "run", "pebble", "osd", "osd-1.json")
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err = os.Stat(receipt)
		if err == nil {
			return cmd
		}
		if time.Now().After(deadline) {
			t.Fatal("OSD child did not publish its process identity")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func suppress(t *testing.T, f fixture) {
	t.Helper()
	ready := filepath.Join(f.common, "data", "osd", "ceph-1", "ready")
	err := os.Rename(ready, ready+".removing")
	if err != nil {
		t.Fatal(err)
	}
}

// TestOSDStopChecksActualExit reproduces a successful repeated Pebble stop while
// the original child is still alive. Cleanup must not trust that acknowledgement.
func TestOSDStopChecksActualExit(t *testing.T) {
	f := newFixture(t)
	cmd := osdChild(t, f, "exec sleep 60\n")
	fakeControl(t, f, `{"services":{"osd-1":{"name":"osd-1","startup":"enabled","current":"inactive"}}}`)
	suppress(t, f)
	out, err := f.command(t, "osd-stop", "1").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "still running") {
		t.Fatalf("accepted a live process after stop acknowledgement: %v: %s", err, out)
	}
	err = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err != nil {
		t.Fatalf("kill child process group: %v", err)
	}
	err = cmd.Wait()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected killed child to report an exit error, got: %v", err)
	}
	out, err = f.command(t, "osd-stop", "1").CombinedOutput()
	if err != nil {
		t.Fatalf("verified stopped OSD rejected: %v: %s", err, out)
	}
}

// TestOSDStopChecksDescendants ensures a dead leader is not proof of group exit.
func TestOSDStopChecksDescendants(t *testing.T) {
	f := newFixture(t)
	cmd := osdChild(t, f, "sleep 60 >/dev/null 2>&1 &\nexit 0\n")
	err := cmd.Wait()
	if err != nil {
		t.Fatalf("wait for successful leader exit: %v", err)
	}
	fakeControl(t, f, `{"services":{"osd-1":{"name":"osd-1","startup":"disabled","current":"inactive"}}}`)
	suppress(t, f)
	out, err := f.command(t, "osd-stop", "1").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "still running") {
		t.Fatalf("accepted surviving process-group member: %v: %s", err, out)
	}
}

// TestOSDStopRequiresFenceAndReceipt rejects eligible or unverifiable targets.
func TestOSDStopRequiresFenceAndReceipt(t *testing.T) {
	f := newFixture(t)
	fakeControl(t, f, `{"services":{"osd-1":{"name":"osd-1","startup":"enabled","current":"inactive"}}}`)
	writeFile(t, filepath.Join(f.common, "data", "osd", "ceph-1", "ready"), "", 0600)
	out, err := f.command(t, "osd-stop", "1").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "autostart is not suppressed") {
		t.Fatalf("stop without eligibility fence: %v: %s", err, out)
	}
	suppress(t, f)
	out, err = f.command(t, "osd-stop", "1").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "process identity") {
		t.Fatalf("missing process identity accepted: %v: %s", err, out)
	}
}

// TestOSDReloadPreservesHealthyServices checks targeted starts and suppression.
func TestOSDReloadPreservesHealthyServices(t *testing.T) {
	f := newFixture(t)
	fakeControl(t, f, `{"services":{"osd-1":{"name":"osd-1","startup":"enabled","current":"active"}}}`)
	for _, entry := range []string{"ceph-1/ready", "ceph-2/ready", "ceph-3/ready", "ceph-3/ready.removing"} {
		writeFile(t, filepath.Join(f.common, "data", "osd", entry), "", 0600)
	}
	out, err := f.command(t, "reload", "osd").CombinedOutput()
	if err != nil {
		t.Fatalf("reload failed: %v: %s", err, out)
	}
	actions, err := os.ReadFile(filepath.Join(f.data, "actions"))
	if err != nil || string(actions) != "start osd-2\n" {
		t.Fatalf("reload disturbed siblings: %v: %s", err, actions)
	}
}
