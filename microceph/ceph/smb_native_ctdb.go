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
	"sync"
	"time"
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

// Retirement publishes stop intent before draining this actor's last operation.
// It is separate from the placement lock so membership can converge while a
// placement waits for native readiness.
var smbMembershipMu sync.Mutex

// Coalesce publication hints; the worker always rereads the active owner's
// latest prepared membership, not a stale event payload. The fallback covers
// daemon restarts and lost hints without a persistent notification protocol.
var smbMembershipChanges = make(chan struct{}, 1)
var notifySMBMembershipChangeFunc = notifySMBMembershipChange

func notifySMBMembershipChange() {
	select {
	case smbMembershipChanges <- struct{}{}:
	default:
	}
}

func runSMBCTDB(ctx context.Context, root, operation string, args ...string) (string, error) {
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
	argv := append([]string{filepath.Join(os.Getenv("SNAP"), "bin/ctdb"), operation}, args...)
	out, err := runSMBPrivateCommandFunc(commandCtx, root, argv, nil)
	return string(out), err
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

func observeSMBCTDBNodes(ctx context.Context, root string, expected []byte, desired map[uint64]string) (bool, smbCTDBStatus, error) {
	output, err := runSMBCTDB(ctx, root, "status")
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
		err = checkSMBCTDBOwner(root)
		if err != nil {
			return err
		}
		err = writeSMBNativeFile(path, expected)
		if err != nil {
			return err
		}
	}
	matches, status, err := observeSMBCTDBNodes(ctx, root, expected, desired)
	if err != nil || matches {
		return err
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
	err = checkSMBCTDBOwner(root)
	if err != nil {
		return err
	}
	_, err = runSMBCTDB(ctx, root, "reloadnodes")
	if err != nil {
		return err
	}
	matches, _, err = observeSMBCTDBNodes(ctx, root, expected, desired)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("CTDB daemon membership does not match published slots")
	}
	return checkSMBCTDBOwner(root)
}

func checkSMBCTDBHealth(ctx context.Context, root string, membership bool) error {
	err := checkSMBCTDBOwner(root)
	if err != nil {
		return err
	}
	launch, err := loadSMBLaunch(root)
	if err != nil {
		return err
	}
	if launch.CTDBPNN == nil {
		return fmt.Errorf("CTDB readiness requires assigned clustered context")
	}
	output, err := runSMBCTDB(ctx, root, "pnn")
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
	_, err = runSMBCTDB(ctx, root, "nodestatus", strconv.FormatUint(pnn, 10))
	if err != nil {
		return err
	}
	output, err = runSMBCTDB(ctx, root, "status")
	if err != nil {
		return err
	}
	_, err = parseSMBCTDBStatus(output)
	if err != nil {
		return err
	}
	if membership {
		expected, desired, err := preparedSMBCTDBNodes(root)
		if err != nil {
			return err
		}
		matches, _, err := observeSMBCTDBNodes(ctx, root, expected, desired)
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
	}
	err = ctx.Err()
	if err != nil {
		return err
	}
	return checkSMBCTDBOwner(root)
}

func reconcileSMBCTDB(ctx context.Context, base string) error {
	wait := time.NewTicker(20 * time.Millisecond)
	defer wait.Stop()
	for !smbMembershipMu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait.C:
		}
	}
	defer smbMembershipMu.Unlock()
	err := ctx.Err()
	if err != nil {
		return err
	}
	root, err := activeSMBRoot(base)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = os.Lstat(filepath.Join(root, "stop-requested"))
	if err == nil {
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	launch, err := loadSMBLaunch(root)
	if os.IsNotExist(err) {
		return nil // The initial projection has not been published yet.
	}
	if err != nil || !launch.Clustered {
		return err
	}
	workCtx, cancel := context.WithTimeout(ctx, smbUpdateTimeout)
	defer cancel()
	err = updateSMBCTDBNodes(workCtx, root)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	path := filepath.Join(root, "diagnostics/nodes-error")
	if err != nil {
		_ = writeSMBNativeFile(path, []byte("CTDB membership update is not confirmed.\n"))
	} else {
		_ = os.Remove(path)
	}
	return err
}
