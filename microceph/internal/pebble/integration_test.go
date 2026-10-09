package pebble_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestPebbleIntegration exercises the adapter against a real, locally supplied
// Pebble binary, with harmless foreground children and no Ceph or Snap dependency.
func TestPebbleIntegration(t *testing.T) {
	binary := os.Getenv("PEBBLE_TEST_BINARY")
	if binary == "" {
		t.Skip("set PEBBLE_TEST_BINARY to the qualified Pebble binary")
	}
	f := newFixture(t)
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.snap, "bin", "pebble"), string(data), 0755)
	data, err = os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.snap, "bin", "microceph-pebble"), string(data), 0755)
	writeFile(t, filepath.Join(f.snap, "commands", "osd.run"), "#!/bin/sh\nexec sleep 60\n", 0755)
	for _, id := range []string{"1", "2"} {
		writeFile(t, filepath.Join(f.common, "data", "osd", "ceph-"+id, "ready"), "", 0600)
	}
	log, err := os.Create(filepath.Join(f.data, "supervisor.log"))
	if err != nil {
		// f.data is normally first created by bootstrap.
		err = os.MkdirAll(f.data, 0700)
		if err != nil {
			t.Fatal(err)
		}
		log, err = os.Create(filepath.Join(f.data, "supervisor.log"))
		if err != nil {
			t.Fatal(err)
		}
	}
	defer log.Close()
	cmd := f.command(t, "run", "osd")
	cmd.Stdout, cmd.Stderr = log, log
	err = cmd.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Termination is best-effort: the supervisor may already have exited.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		// Emergency cleanup if a supervisor failure left a child group behind.
		paths, _ := filepath.Glob(filepath.Join(f.common, "run", "pebble", "osd", "osd-*.json"))
		for _, path := range paths {
			var process struct{ PID int }
			data, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("read cleanup process receipt %s: %v", path, err)
				continue
			}
			err = json.Unmarshal(data, &process)
			if err != nil {
				t.Errorf("decode cleanup process receipt %s: %v", path, err)
				continue
			}
			if process.PID > 1 {
				// Normal supervisor shutdown will already have removed this group.
				_ = syscall.Kill(-process.PID, syscall.SIGKILL)
			}
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err = f.command(t, "status", "osd-2").CombinedOutput()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(log.Name())
			t.Fatalf("supervisor did not start: %v: %s", err, data)
		}
		time.Sleep(20 * time.Millisecond)
	}
	out, err := f.command(t, "reload", "osd").CombinedOutput()
	if err != nil {
		t.Fatalf("initial reconcile: %v: %s", err, out)
	}
	// Wait for the Go child to register before comparing identities.
	path := filepath.Join(f.common, "run", "pebble", "osd", "osd-2.json")
	for {
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	sibling := string(data)
	writeFile(t, filepath.Join(f.common, "data", "osd", "ceph-3", "ready"), "", 0600)
	out, err = f.command(t, "reload", "osd").CombinedOutput()
	if err != nil {
		t.Fatalf("add/reload OSD: %v: %s", err, out)
	}
	suppress(t, f)
	out, err = f.command(t, "osd-stop", "1").CombinedOutput()
	if err != nil {
		t.Fatalf("named stop and exit verification: %v: %s", err, out)
	}
	out, err = f.command(t, "reload", "osd").CombinedOutput()
	if err != nil {
		t.Fatalf("reconcile after suppression: %v: %s", err, out)
	}
	_, err = f.command(t, "status", "osd-1").CombinedOutput()
	if err == nil {
		t.Fatal("suppressed OSD restarted")
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != sibling {
		t.Fatalf("healthy sibling restarted: %v", err)
	}
}
