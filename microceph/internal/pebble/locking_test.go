package pebble_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestBootstrapSerializesWithController checks the shared controller lock.
func TestBootstrapSerializesWithController(t *testing.T) {
	testControllerLock(t, "run", "osd")
}

// TestPublicationSerializesWithController prevents a late ready publication
// from racing bootstrap, reload, or retirement of a service.
func TestPublicationSerializesWithController(t *testing.T) {
	testControllerLock(t, "osd-ready", "7")
}

func testControllerLock(t *testing.T, operation, target string) {
	t.Helper()
	f := newFixture(t)
	writeFile(t, filepath.Join(f.common, "data", "osd", "ceph-7", "fsid"), "test", 0600)
	path := filepath.Join(f.common, "run", "pebble", "osd", "control.lock")
	writeFile(t, path, "", 0600)
	guard, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	err = syscall.Flock(int(guard.Fd()), syscall.LOCK_EX)
	if err != nil {
		t.Fatal(err)
	}
	cmd := f.command(t, operation, target)
	err = cmd.Start()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { cmd.Process.Kill() })
	select {
	case err := <-done:
		t.Fatalf("bootstrap bypassed controller lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	err = syscall.Flock(int(guard.Fd()), syscall.LOCK_UN)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bootstrap did not resume after controller released the lock")
	}
}
