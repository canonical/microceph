package ceph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/canonical/microceph/microceph/common"
)

type smbCTDBNode struct {
	Address string
	Flags   string
}
type smbCTDBStatus struct {
	Slots uint64
	Nodes map[uint64]smbCTDBNode
}

var smbCTDBCountPattern = regexp.MustCompile(`^Number of nodes:(\d+)(?: \(including \d+ deleted nodes\))?$`)
var smbCTDBNodePattern = regexp.MustCompile(`^pnn:(\d+)\s+(\S+)\s+(.+)$`)

func runSMBCTDB(ctx context.Context, operation string, args ...string) (string, error) {
	switch operation {
	case "pnn", "nodestatus", "status", "reloadnodes":
	default:
		return "", fmt.Errorf("unsupported native CTDB control command")
	}
	if len(args) > 0 {
		if operation != "nodestatus" || len(args) != 1 {
			return "", fmt.Errorf("invalid CTDB control argument")
		}
		_, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return "", fmt.Errorf("invalid CTDB control argument")
		}
	}
	commandCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return common.ProcessExec.RunCommandContext(commandCtx, filepath.Join(os.Getenv("SNAP"), "bin/ctdb"), append([]string{operation}, args...)...)
}

func parseSMBCTDBStatus(text string) (smbCTDBStatus, error) {
	result := smbCTDBStatus{Nodes: map[uint64]smbCTDBNode{}}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	counts := 0
	normal := false
	for _, line := range lines {
		if strings.HasPrefix(line, "Number of nodes:") {
			match := smbCTDBCountPattern.FindStringSubmatch(line)
			if len(match) != 2 {
				return result, fmt.Errorf("invalid CTDB membership status")
			}
			var err error
			result.Slots, err = strconv.ParseUint(match[1], 10, 64)
			if err != nil {
				return result, err
			}
			counts++
		}
		normal = normal || line == "Recovery mode:NORMAL (0)"
	}
	if counts != 1 || result.Slots == 0 || result.Slots > 65536 {
		return result, fmt.Errorf("invalid CTDB membership slot count")
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "pnn:") {
			continue
		}
		match := smbCTDBNodePattern.FindStringSubmatch(line)
		if len(match) != 4 {
			return result, fmt.Errorf("invalid CTDB membership node")
		}
		pnn, err := strconv.ParseUint(match[1], 10, 64)
		if err != nil {
			return result, err
		}
		_, exists := result.Nodes[pnn]
		ip := net.ParseIP(match[2])
		if exists || pnn >= result.Slots || ip == nil {
			return result, fmt.Errorf("invalid CTDB membership rank/address")
		}
		result.Nodes[pnn] = smbCTDBNode{Address: ip.String(), Flags: strings.TrimSuffix(match[3], " (THIS NODE)")}
	}
	if !normal {
		return result, fmt.Errorf("CTDB recovery is not normal")
	}
	return result, nil
}

func preparedSMBCTDBNodes(root string) ([]byte, map[uint64]string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "config/candidate.json"))
	if err != nil {
		return nil, nil, err
	}
	var candidate smbCandidate
	err = json.Unmarshal(raw, &candidate)
	if err != nil {
		return nil, nil, err
	}
	if !candidate.Spec.hasFeature("clustered") || candidate.Spec.ClusterID != filepath.Base(root) {
		return nil, nil, fmt.Errorf("node monitor requires its validated clustered projection")
	}
	nodes, err := smbCTDBNodes(candidate)
	if err != nil {
		return nil, nil, err
	}
	desired := map[uint64]string{}
	for pnn, line := range strings.Split(strings.TrimSuffix(string(nodes), "\n"), "\n") {
		if !strings.HasPrefix(line, "#") {
			desired[uint64(pnn)] = net.ParseIP(line).String()
		}
	}
	return nodes, desired, nil
}

func observeSMBCTDBNodes(ctx context.Context, expected []byte, desired map[uint64]string) (bool, smbCTDBStatus, error) {
	output, err := runSMBCTDB(ctx, "status")
	if err != nil {
		return false, smbCTDBStatus{}, err
	}
	status, err := parseSMBCTDBStatus(output)
	if err != nil {
		return false, status, err
	}
	matches := status.Slots == uint64(bytes.Count(expected, []byte("\n"))) && len(status.Nodes) == len(desired)
	for pnn, node := range status.Nodes {
		matches = matches && desired[pnn] == node.Address
	}
	return matches, status, nil
}

func updateSMBCTDBNodes(ctx context.Context, root string) error {
	err := checkSMBCTDBOwner(root)
	if err != nil {
		return err
	}
	expected, desired, err := preparedSMBCTDBNodes(root)
	if err != nil {
		return err
	}
	path := filepath.Join(root, "ctdb/nodes")
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, expected) {
		err = writeSMBNativeFile(path, expected)
		if err != nil {
			return err
		}
	}
	matches, status, err := observeSMBCTDBNodes(ctx, expected, desired)
	if err != nil {
		return err
	}
	if matches {
		return nil
	}
	for pnn, node := range status.Nodes {
		address, exists := desired[pnn]
		if exists {
			if address != node.Address || node.Flags != "OK" {
				return fmt.Errorf("CTDB surviving membership is unhealthy or changed address")
			}
		} else {
			disconnected := false
			for _, flag := range strings.Split(node.Flags, "|") {
				disconnected = disconnected || flag == "DISCONNECTED"
			}
			if !disconnected {
				return fmt.Errorf("CTDB retired membership is still connected")
			}
		}
	}
	_, err = runSMBCTDB(ctx, "reloadnodes")
	if err != nil {
		return err
	}
	matches, _, err = observeSMBCTDBNodes(ctx, expected, desired)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("CTDB daemon membership does not match published slots")
	}
	return nil
}

func checkSMBCTDBHealth(ctx context.Context, root string, monitor bool) error {
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	if !launch.Clustered || launch.CTDBPNN == nil {
		return fmt.Errorf("CTDB readiness requires assigned clustered context")
	}
	output, err := runSMBCTDB(ctx, "pnn")
	if err != nil {
		return err
	}
	pnn, err := strconv.ParseUint(strings.TrimSpace(output), 10, 64)
	if err != nil {
		return err
	}
	if pnn != *launch.CTDBPNN {
		return fmt.Errorf("CTDB assigned PNN mismatch")
	}
	_, err = runSMBCTDB(ctx, "nodestatus", strconv.FormatUint(pnn, 10))
	if err != nil {
		return err
	}
	output, err = runSMBCTDB(ctx, "status")
	if err != nil {
		return err
	}
	_, err = parseSMBCTDBStatus(output)
	if err != nil {
		return err
	}
	if monitor {
		expected, desired, err := preparedSMBCTDBNodes(root)
		if err != nil {
			return err
		}
		matches, _, err := observeSMBCTDBNodes(ctx, expected, desired)
		if err != nil {
			return err
		}
		current, err := os.ReadFile(filepath.Join(root, "ctdb/nodes"))
		if err != nil {
			return err
		}
		if !matches || !bytes.Equal(current, expected) {
			return fmt.Errorf("CTDB daemon membership is not confirmed")
		}
		data, err := os.ReadFile(filepath.Join(root, "ctdb/run/ctdb-nodes.pid"))
		if err != nil {
			return err
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			return err
		}
		if pid <= 0 {
			return fmt.Errorf("invalid node monitor PID")
		}
		err = unix.Kill(pid, 0)
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func openSMBCTDBListener(root string) (*net.UnixListener, *os.File, error) {
	path := filepath.Join(root, "ctdb/run/native-ready.socket")
	fd, err := unix.Open(filepath.Join(root, "ctdb/run/native-ready.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, nil, err
	}
	guard := os.NewFile(uintptr(fd), "native CTDB readiness lock")
	fail := func(err error) (*net.UnixListener, *os.File, error) { _ = guard.Close(); return nil, nil, err }
	info, err := guard.Stat()
	if err != nil {
		return fail(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Uid != uint32(os.Geteuid()) {
		return fail(fmt.Errorf("unsafe CTDB readiness lock"))
	}
	err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
	if err != nil {
		return fail(err)
	}
	info, err = os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return fail(err)
	}
	if err == nil {
		stat, ok = info.Sys().(*syscall.Stat_t)
		if !ok || info.Mode()&os.ModeSocket == 0 || stat.Uid != uint32(os.Geteuid()) {
			return fail(fmt.Errorf("unsafe CTDB readiness socket"))
		}
		err = os.Remove(path)
		if err != nil {
			return fail(err)
		}
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return fail(err)
	}
	err = os.Chmod(path, 0600)
	if err != nil {
		_ = listener.Close()
		return fail(err)
	}
	return listener, guard, nil
}

func monitorSMBCTDB(ctx context.Context, root string) error {
	err := checkSMBCTDBOwner(root)
	if err != nil {
		return err
	}
	listener, guard, err := openSMBCTDBListener(root)
	if err != nil {
		return err
	}
	defer guard.Close()
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	pidfile := filepath.Join(root, "ctdb/run/ctdb-nodes.pid")
	diagnostic := filepath.Join(root, "diagnostics/nodes-error")
	defer os.Remove(pidfile)
	for ctx.Err() == nil {
		err = updateSMBCTDBNodes(ctx, root)
		if err == nil {
			err = checkSMBCTDBHealth(ctx, root, false)
		}
		if err == nil {
			err = writeSMBNativeFile(pidfile, []byte(strconv.Itoa(os.Getpid())+"\n"))
		}
		if err == nil {
			_ = os.Remove(diagnostic)
		} else {
			_ = os.Remove(pidfile)
			_ = writeSMBNativeFile(diagnostic, []byte("CTDB membership update is not confirmed.\n"))
		}
		err = listener.SetDeadline(time.Now().Add(2 * time.Second))
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		conn, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			networkErr, ok := err.(net.Error)
			if ok && networkErr.Timeout() {
				continue
			}
			return err
		}
		err = checkSMBCTDBOwner(root)
		if err == nil {
			err = checkSMBCTDBHealth(ctx, root, true)
		}
		if err == nil {
			err = checkSMBCTDBOwner(root)
		}
		if err == nil && ctx.Err() == nil {
			err = conn.SetWriteDeadline(time.Now().Add(time.Second))
			if err == nil {
				_, _ = conn.Write([]byte("1"))
			}
		}
		_ = conn.Close()
	}
	return nil
}
