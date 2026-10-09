package pebble_test

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOSDPublication verifies the authoritative marker, not only a supervisor plan.
func TestOSDPublication(t *testing.T) {
	for _, fenced := range []bool{false, true} {
		name := "eligible"
		if fenced {
			name = "removing"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			path := filepath.Join(f.common, "data", "osd", "ceph-7")
			writeFile(t, filepath.Join(path, "fsid"), "test", 0600)
			if fenced {
				writeFile(t, filepath.Join(path, "ready.removing"), "", 0600)
			}
			out, err := f.command(t, "osd-ready", "7").CombinedOutput()
			if fenced {
				if err == nil {
					t.Fatal("publication overrode removal fence")
				}
				_, err = os.Stat(filepath.Join(path, "ready"))
				if !os.IsNotExist(err) {
					t.Fatalf("published fenced OSD: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("publication failed: %v: %s", err, out)
			}
			info, err := os.Stat(filepath.Join(path, "ready"))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("ready marker missing or not private: %v", err)
			}
		})
	}
}
