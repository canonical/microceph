// Package pebble implements MicroCeph's private per-Snap-app supervisor adapter.
// It uses the bundled Pebble CLI, not Pebble's Go module or a public Snap app.
package pebble

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Runtime holds the active Snap revision's paths. Layers are disposable;
// existing Ceph data and ready markers remain authoritative.
type Runtime struct {
	// Snap is the active read-only revision mount.
	Snap string
	// Data is the revision-specific writable directory.
	Data string
	// Common is the revision-independent writable directory.
	Common string
}

// FromEnvironment reads and validates the paths supplied by Snapd.
func FromEnvironment() (Runtime, error) {
	r := Runtime{Snap: os.Getenv("SNAP"), Data: os.Getenv("SNAP_DATA"), Common: os.Getenv("SNAP_COMMON")}
	for name, value := range map[string]string{"SNAP": r.Snap, "SNAP_DATA": r.Data, "SNAP_COMMON": r.Common} {
		if !filepath.IsAbs(value) {
			return Runtime{}, fmt.Errorf("%s must be an absolute Snap path", name)
		}
	}
	return r, nil
}

func validApp(app string) bool {
	switch app {
	case "daemon", "mon", "mgr", "mds", "osd", "rgw", "nfs", "rbd-mirror", "cephfs-mirror":
		return true
	default:
		return false
	}
}

func (r Runtime) root(app string) string {
	return filepath.Join(r.Data, "pebble", app)
}

func (r Runtime) socketDir(app string) string {
	return filepath.Join(r.Common, "run", "pebble", app)
}

func (r Runtime) environment(app string) []string {
	environment := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "PEBBLE" && key != "PEBBLE_SOCKET" && key != "PEBBLE_PERSIST" {
			environment = append(environment, entry)
		}
	}
	return append(environment, "PEBBLE="+r.root(app),
		"PEBBLE_SOCKET="+filepath.Join(r.socketDir(app), ".pebble.socket"), "PEBBLE_PERSIST=never")
}

func (r Runtime) prepare(app string) error {
	for _, path := range []string{r.root(app), filepath.Join(r.root(app), "layers"), r.socketDir(app)} {
		err := os.MkdirAll(path, 0700)
		if err != nil {
			return err
		}
		err = os.Chmod(path, 0700)
		if err != nil {
			return err
		}
	}
	return nil
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	err := os.WriteFile(tmp, data, 0600)
	if err != nil {
		return err
	}
	err = os.Chmod(tmp, 0600)
	if err != nil {
		os.Remove(tmp)
		return err
	}
	err = os.Rename(tmp, path)
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// JSON is also valid YAML. Using the standard encoder makes quoting explicit
// and avoids adding a YAML or Pebble module dependency to the adapter.
type layer struct {
	Services map[string]map[string]string `json:"services"`
}

func (r Runtime) service(app, id string) map[string]string {
	wrapper := app
	if app == "nfs" {
		wrapper = "nfs-ganesha"
	}
	command := strconv.Quote(filepath.Join(r.Snap, "commands", wrapper+".start"))
	delay := "30s"
	if app == "osd" {
		command = strconv.Quote(filepath.Join(r.Snap, "bin", "microceph-pebble")) + " osd-run " + id
		delay = "5m"
	}
	return map[string]string{"override": "replace", "command": command, "startup": "enabled",
		"on-success": "ignore", "on-failure": "restart", "kill-delay": delay}
}

func validOSDID(id string) bool {
	n, err := strconv.ParseInt(id, 10, 64)
	return err == nil && n >= 0 && strconv.FormatInt(n, 10) == id
}

func (r Runtime) eligible(id string) (bool, error) {
	path := filepath.Join(r.Common, "data", "osd", "ceph-"+id)
	_, err := os.Lstat(filepath.Join(path, "ready.removing"))
	if err == nil {
		return false, nil
	}
	if !os.IsNotExist(err) {
		return false, err
	}
	info, err := os.Lstat(filepath.Join(path, "ready"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

func (r Runtime) inventory() (layer, error) {
	result := layer{Services: map[string]map[string]string{}}
	entries, err := os.ReadDir(filepath.Join(r.Common, "data", "osd"))
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || len(name) <= 5 || name[:5] != "ceph-" || !validOSDID(name[5:]) {
			continue
		}
		id := name[5:]
		ready, err := r.eligible(id)
		if err != nil {
			return result, err
		}
		if ready {
			result.Services["osd-"+id] = r.service("osd", id)
		}
	}
	return result, nil
}

// Run generates the current revision's layer, then replaces this process with
// Pebble. The existing singleton wrappers still construct each Ceph command.
func (r Runtime) Run(ctx context.Context, app string) error {
	if !validApp(app) {
		return fmt.Errorf("unsupported Pebble app %q", app)
	}
	err := r.prepare(app)
	if err != nil {
		return err
	}
	guard, err := lock(ctx, filepath.Join(r.socketDir(app), "control.lock"))
	if err != nil {
		return err
	}
	// os.OpenFile uses close-on-exec. The lock is released at successful exec,
	// or on return here if layer generation/exec fails. A controller reaching
	// the socket before the new supervisor is ready fails closed.
	defer guard.Close()
	plan := layer{Services: map[string]map[string]string{app: r.service(app, "")}}
	if app == "osd" {
		plan, err = r.inventory()
		if err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	err = atomicWrite(filepath.Join(r.root(app), "layers", "001-microceph-"+app+".yaml"), append(data, '\n'))
	if err != nil {
		return err
	}
	binary := filepath.Join(r.Snap, "bin", "pebble")
	return syscall.Exec(binary, []string{binary, "run", "--verbose"}, r.environment(app))
}
