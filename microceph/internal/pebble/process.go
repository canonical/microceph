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

type processIdentity struct {
	PID       int    `json:"pid"`
	StartTime string `json:"start-time"`
	BootID    string `json:"boot-id"`
}

type processStat struct {
	group     int
	state     string
	startTime string
}

func readStat(pid int) (processStat, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return processStat{}, err
	}
	// comm is parenthesized and may itself contain spaces or parentheses.
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return processStat{}, fmt.Errorf("invalid /proc/%d/stat", pid)
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return processStat{}, fmt.Errorf("short /proc/%d/stat", pid)
	}
	group, err := strconv.Atoi(fields[2])
	if err != nil {
		return processStat{}, err
	}
	return processStat{group: group, state: fields[0], startTime: fields[19]}, nil
}

func bootID() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(data)), err
}

func (p processIdentity) exited() (bool, error) {
	if p.PID <= 1 || p.StartTime == "" || p.BootID == "" {
		return false, fmt.Errorf("invalid process identity")
	}
	boot, err := bootID()
	if err != nil {
		return false, err
	}
	if boot != p.BootID {
		return true, nil
	}
	leader, err := readStat(p.PID)
	if err == nil {
		if leader.startTime != p.StartTime {
			// A PID cannot be recycled while its old process group still exists.
			return true, nil
		}
		if leader.state != "Z" && leader.state != "X" {
			return false, nil
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}
	// A shell may exit while an unlock helper is still running in its group.
	// Do not treat the leader's exit as permission to delete storage.
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || !entry.IsDir() {
			continue
		}
		stat, err := readStat(pid)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		if stat.group == p.PID && stat.state != "Z" && stat.state != "X" {
			return false, nil
		}
	}
	return true, nil
}

func (r Runtime) processPath(id string) string {
	return filepath.Join(r.socketDir("osd"), "osd-"+id+".json")
}

func (r Runtime) readProcess(id string) (processIdentity, error) {
	var process processIdentity
	data, err := os.ReadFile(r.processPath(id))
	if err != nil {
		return process, err
	}
	err = json.Unmarshal(data, &process)
	return process, err
}

func (r Runtime) recordProcess(ctx context.Context, id string) error {
	guard, err := lock(ctx, r.processPath(id)+".lock")
	if err != nil {
		return err
	}
	defer guard.Close()
	previous, err := r.readProcess(id)
	if err == nil {
		exited, err := previous.exited()
		if err != nil {
			return err
		}
		if !exited {
			return fmt.Errorf("previous osd.%s process group is still running", id)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	pid := os.Getpid()
	stat, err := readStat(pid)
	if err != nil {
		return err
	}
	if stat.group != pid {
		return fmt.Errorf("OSD wrapper must be launched as a Pebble process-group leader")
	}
	boot, err := bootID()
	if err != nil {
		return err
	}
	data, err := json.Marshal(processIdentity{PID: pid, StartTime: stat.startTime, BootID: boot})
	if err != nil {
		return err
	}
	return atomicWrite(r.processPath(id), data)
}

// RunOSD records its process identity before checking eligibility, then execs
// the per-OSD wrapper. The PID/start time survives both wrapper and Ceph execs.
func (r Runtime) RunOSD(ctx context.Context, id string) error {
	if !validOSDID(id) {
		return fmt.Errorf("invalid OSD ID %q", id)
	}
	err := r.prepare("osd")
	if err != nil {
		return err
	}
	err = r.recordProcess(ctx, id)
	if err != nil {
		return err
	}
	ready, err := r.eligible(id)
	if err != nil {
		return err
	}
	if !ready {
		// A layer prepared before a removal fence may still invoke this wrapper.
		// A successful no-op prevents an automatic restart loop for that OSD.
		return nil
	}
	wrapper := filepath.Join(r.Snap, "commands", "osd.run")
	return syscall.Exec(wrapper, []string{wrapper, id}, os.Environ())
}
