package pebble

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

type serviceInfo struct {
	Name    string `json:"name"`
	Startup string `json:"startup"`
	Current string `json:"current"`
}

func (r Runtime) command(ctx context.Context, app string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, filepath.Join(r.Snap, "bin", "pebble"), args...)
	cmd.Env = r.environment(app)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("Pebble %s: %w: %s", args[0], err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("Pebble %s: %w", args[0], err)
	}
	return out, nil
}

func (r Runtime) services(ctx context.Context, app string) (map[string]serviceInfo, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := r.command(queryCtx, app, "services", "--format=json")
	if err != nil {
		return nil, err
	}
	var response struct {
		Services map[string]serviceInfo `json:"services"`
	}
	err = json.Unmarshal(out, &response)
	if err != nil {
		return nil, fmt.Errorf("invalid Pebble service status: %w", err)
	}
	if response.Services == nil {
		return nil, fmt.Errorf("missing Pebble service status")
	}
	return response.Services, nil
}

// waitServices handles the gap between execing a new supervisor and its API
// becoming available. It must not turn a transient startup race into a lost reload.
func (r Runtime) waitServices(ctx context.Context, app string) (map[string]serviceInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		services, err := r.services(ctx, app)
		if err == nil {
			return services, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for %s supervisor: %w: %v", app, ctx.Err(), err)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func appForService(name string) (string, error) {
	if strings.HasPrefix(name, "osd-") && validOSDID(strings.TrimPrefix(name, "osd-")) {
		return "osd", nil
	}
	if !validApp(name) || name == "osd" {
		return "", fmt.Errorf("unsupported Pebble service %q", name)
	}
	return name, nil
}

// CheckActive checks the named child, not merely its live Pebble supervisor.
// OSD names are osd-<id>; singleton names match their existing Snap app names.
func (r Runtime) CheckActive(ctx context.Context, name string) error {
	app, err := appForService(name)
	if err != nil {
		return err
	}
	services, err := r.services(ctx, app)
	if err != nil {
		return err
	}
	service, ok := services[name]
	if !ok || service.Name != name || service.Current != "active" {
		return fmt.Errorf("%s child is not active (state %q)", name, service.Current)
	}
	return nil
}

// WaitReady synchronizes a Snap start with the named child's initial startup.
// It is bounded and rejects backoff/error rather than hiding a failed child.
func (r Runtime) WaitReady(ctx context.Context, name string) error {
	app, err := appForService(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var lastErr error
	for {
		services, err := r.services(ctx, app)
		lastErr = err
		if err == nil {
			service := services[name]
			if service.Name == name && service.Current == "active" {
				return nil
			}
			lastErr = fmt.Errorf("%s child is not active (state %q)", name, service.Current)
			if service.Current != "" && service.Current != "inactive" {
				return lastErr
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w: %v", name, ctx.Err(), lastErr)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// lock serializes controller operations across processes and Snap revisions.
// Closing the file releases the lock, including when the controller is killed.
func lock(ctx context.Context, path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return file, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (r Runtime) addLayer(ctx context.Context, plan layer) error {
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	// This is an API input, not a layer to load on the next app start.
	path := filepath.Join(r.root("osd"), "update.yaml")
	err = atomicWrite(path, data)
	if err != nil {
		return err
	}
	_, err = r.command(ctx, "osd", "add", "--combine", "microceph", path)
	return err
}

// ReloadOSDs adds and starts newly eligible OSDs without replanning healthy
// siblings. A failed OSD must not prevent later eligible OSDs from starting.
func (r Runtime) ReloadOSDs(ctx context.Context) error {
	err := r.prepare("osd")
	if err != nil {
		return err
	}
	guard, err := lock(ctx, filepath.Join(r.socketDir("osd"), "control.lock"))
	if err != nil {
		return err
	}
	defer guard.Close()
	services, err := r.waitServices(ctx, "osd")
	if err != nil {
		return err
	}
	inventory, err := r.inventory()
	if err != nil {
		return err
	}
	pending := layer{Services: map[string]map[string]string{}}
	for name, service := range inventory.Services {
		current, exists := services[name]
		// Backoff already has a pending restart. Do not reset its restart loop.
		if exists && current.Startup == "enabled" && (current.Current == "active" || current.Current == "backoff") {
			continue
		}
		pending.Services[name] = service
	}
	if len(pending.Services) == 0 {
		return nil
	}
	err = r.addLayer(ctx, pending)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(pending.Services))
	for name := range pending.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	var failures []error
	for _, name := range names {
		_, err = r.command(ctx, "osd", "start", name)
		if err != nil {
			failures = append(failures, fmt.Errorf("start %s: %w", name, err))
		}
	}
	return errors.Join(failures...)
}

// PublishOSD publishes a ready marker under the same lock as bootstrap,
// reload, and stop. A removal fence always wins, including after a lost reply.
func (r Runtime) PublishOSD(ctx context.Context, id string) error {
	if !validOSDID(id) {
		return fmt.Errorf("invalid OSD ID %q", id)
	}
	err := r.prepare("osd")
	if err != nil {
		return err
	}
	guard, err := lock(ctx, filepath.Join(r.socketDir("osd"), "control.lock"))
	if err != nil {
		return err
	}
	defer guard.Close()
	path := filepath.Join(r.Common, "data", "osd", "ceph-"+id)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("osd.%s data path is not a directory", id)
	}
	_, err = os.Lstat(filepath.Join(path, "ready.removing"))
	if err == nil {
		return fmt.Errorf("osd.%s is fenced for removal", id)
	}
	if !os.IsNotExist(err) {
		return err
	}
	return atomicWrite(filepath.Join(path, "ready"), nil)
}

// StopOSD retires a named service only after its ready marker is suppressed.
// Neither an inactive status nor a successful repeated stop proves process exit.
// Any uncertain result is an error: callers must preserve storage and the fence.
func (r Runtime) StopOSD(ctx context.Context, id string) error {
	if !validOSDID(id) {
		return fmt.Errorf("invalid OSD ID %q", id)
	}
	err := r.prepare("osd")
	if err != nil {
		return err
	}
	guard, err := lock(ctx, filepath.Join(r.socketDir("osd"), "control.lock"))
	if err != nil {
		return err
	}
	defer guard.Close()
	ready, err := r.eligible(id)
	if err != nil {
		return err
	}
	if ready {
		return fmt.Errorf("osd.%s autostart is not suppressed", id)
	}
	services, err := r.services(ctx, "osd")
	if err != nil {
		return err
	}
	name := "osd-" + id
	_, exists := services[name]
	if exists {
		err = r.addLayer(ctx, layer{Services: map[string]map[string]string{
			name: {"override": "merge", "startup": "disabled"},
		}})
		if err != nil {
			return err
		}
		_, err = r.command(ctx, "osd", "stop", name)
		if err != nil {
			return err
		}
	}
	process, err := r.readProcess(id)
	if os.IsNotExist(err) && !exists {
		// Never published in this supervisor. RunOSD records its identity before
		// checking eligibility, so a late wrapper cannot launch after this fence.
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot verify osd.%s process identity: %w", id, err)
	}
	exited, err := process.exited()
	if err != nil {
		return fmt.Errorf("cannot verify osd.%s exit: %w", id, err)
	}
	if !exited {
		return fmt.Errorf("osd.%s process group is still running; preserve storage and retry after exit", id)
	}
	return nil
}
