package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"

	"github.com/tidwall/gjson"
)

func createMonMap(s interfaces.StateInterface, path string, fsid string, address string) error {
	// Generate initial monitor map.
	err := genMonmap(filepath.Join(path, "mon.map"), fsid)
	if err != nil {
		return fmt.Errorf("failed to generate monitor map: %w", err)
	}

	err = addMonmap(filepath.Join(path, "mon.map"), s.ClusterState().Name(), address)
	if err != nil {
		return fmt.Errorf("failed to add monitor map: %w", err)
	}

	return nil
}

func genMonmap(path string, fsid string) error {
	// A persistent Nautilus feature makes --add write the initial monitor as a
	// v2/v1 address vector. Starting with a v1-only map and upgrading it after
	// the monitor starts leaves Ceph 20 reporting MON_MSGR2_NOT_ENABLED.
	args := []string{
		"--create",
		"--feature-set", "nautilus",
		"--persistent",
		"--fsid", fsid,
		path,
	}

	_, err := common.ProcessExec.RunCommand("monmaptool", args...)
	if err != nil {
		return err
	}

	return nil
}

func addMonmap(path string, name string, address string) error {
	args := []string{
		"--add",
		name,
		address,
		path,
	}

	_, err := common.ProcessExec.RunCommand("monmaptool", args...)
	if err != nil {
		return err
	}

	return nil
}

func bootstrapMon(hostname string, path string, monmap string, keyring string) error {
	args := []string{
		"--mkfs",
		"-i", hostname,
		"--mon-data", path,
		"--monmap", monmap,
		"--keyring", keyring,
	}

	_, err := common.ProcessExec.RunCommand("ceph-mon", args...)
	if err != nil {
		return err
	}

	return nil
}

func joinMon(hostname string, path string) error {
	tmpPath, err := os.MkdirTemp("", "")
	if err != nil {
		return fmt.Errorf("unable to create temporary path: %w", err)
	}
	defer os.RemoveAll(tmpPath)

	monmap := filepath.Join(tmpPath, "mon.map")
	_, err = cephRun("mon", "getmap", "-o", monmap)
	if err != nil {
		return fmt.Errorf("failed to retrieve monmap: %w", err)
	}

	keyring := filepath.Join(tmpPath, "mon.keyring")
	_, err = cephRun("auth", "get", "mon.", "-o", keyring)
	if err != nil {
		return fmt.Errorf("failed to retrieve mon keyring: %w", err)
	}

	return bootstrapMon(hostname, path, monmap, keyring)
}

// monRemovalVerifyTimeout bounds the detached postcondition check after an
// ambiguous `ceph mon rm` result. The command may have committed just as its
// caller's context expired, so checking with the cancelled context would turn
// a successful removal into an ambiguous failure.
const monRemovalVerifyTimeout = 30 * time.Second

// getMonmapNames returns the monitor names in the committed monmap.
func getMonmapNames(ctx context.Context) ([]string, error) {
	output, err := cephRunContext(ctx, "mon", "dump", "-f", "json")
	if err != nil {
		return nil, fmt.Errorf("failed to read monitor map: %w", err)
	}

	var dump struct {
		Mons json.RawMessage `json:"mons"`
	}
	if err := json.Unmarshal([]byte(output), &dump); err != nil {
		return nil, fmt.Errorf("failed to parse monitor map: %w", err)
	}
	if len(dump.Mons) == 0 {
		return nil, fmt.Errorf("failed to parse monitor map: missing %q field", "mons")
	}

	var mons *[]struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(dump.Mons, &mons); err != nil {
		return nil, fmt.Errorf("failed to parse monitor map: invalid %q field: %w", "mons", err)
	}
	if mons == nil {
		return nil, fmt.Errorf("failed to parse monitor map: %q must be a non-null array", "mons")
	}
	if len(*mons) == 0 {
		return nil, fmt.Errorf("failed to parse monitor map: %q must contain at least one monitor", "mons")
	}

	names := make([]string, 0, len(*mons))
	for i, mon := range *mons {
		if mon.Name == "" {
			return nil, fmt.Errorf("failed to parse monitor map: monitor at index %d has no name", i)
		}
		names = append(names, mon.Name)
	}
	return names, nil
}

// monInMonmap reports whether hostname is present in the committed monmap.
func monInMonmap(ctx context.Context, hostname string) (bool, error) {
	names, err := getMonmapNames(ctx)
	if err != nil {
		return false, err
	}
	for _, name := range names {
		if name == hostname {
			return true, nil
		}
	}
	return false, nil
}

// getMonQuorumNames returns the monitor names in the current Ceph quorum.
func getMonQuorumNames(ctx context.Context) ([]string, error) {
	output, err := cephRunContext(ctx, "mon", "stat", "-f", "json")
	if err != nil {
		return nil, fmt.Errorf("failed to run 'ceph mon stat': %w", err)
	}

	var stat struct {
		QuorumNames []string `json:"quorum_names"`
		Quorum      []struct {
			Name string `json:"name"`
		} `json:"quorum"`
	}
	if err := json.Unmarshal([]byte(output), &stat); err != nil {
		return nil, fmt.Errorf("failed to parse 'ceph mon stat' output: %w", err)
	}
	if len(stat.QuorumNames) > 0 {
		return stat.QuorumNames, nil
	}

	names := make([]string, 0, len(stat.Quorum))
	for _, mon := range stat.Quorum {
		names = append(names, mon.Name)
	}
	return names, nil
}

// removeMon removes a monitor from the committed monmap while respecting the
// caller's deadline.
func removeMon(ctx context.Context, hostname string) error {
	_, err := cephRunContext(ctx, "mon", "rm", hostname)
	if err != nil {
		logger.Errorf("failed to remove monitor %q: %v", hostname, err)
		return fmt.Errorf("failed to remove monitor %q: %w", hostname, err)
	}
	return nil
}

// verifyMonAbsent checks the monmap with a fresh, bounded context after an
// ambiguous `ceph mon rm` result. This makes a removal resumable even when the
// original request was cancelled while the command response was in flight.
func verifyMonAbsent(ctx context.Context, hostname string) (bool, error) {
	verifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), monRemovalVerifyTimeout)
	defer cancel()

	present, err := monInMonmap(verifyCtx, hostname)
	return !present, err
}

// ensureMonAbsent commits MON membership removal before the local daemon is
// stopped. Stopping first can destroy quorum in a two-to-one migration and
// leave the quorum-dependent `ceph mon rm` command unable to complete.
//
// The operation is deliberately idempotent. If an earlier attempt committed
// the monmap change but failed during local teardown, the still-present service
// database row causes reconciliation to call this again; an already-absent MON
// then proceeds directly to stop, cleanup, and database removal.
func ensureMonAbsent(ctx context.Context, hostname string) error {
	monmap, err := getMonmapNames(ctx)
	if err != nil {
		return err
	}
	present := false
	monmapSet := make(map[string]bool, len(monmap))
	for _, name := range monmap {
		monmapSet[name] = true
		if name == hostname {
			present = true
		}
	}
	if !present {
		return nil
	}

	quorum, err := getMonQuorumNames(ctx)
	if err != nil {
		return err
	}
	// The committed post-removal monmap has len(monmap)-1 members and needs a
	// majority of those members to remain in quorum. Merely finding one other
	// quorum member is insufficient for a degraded larger monmap: removing A
	// from map {A,B,C} with quorum {A,B} would leave {B,C}, which needs both B
	// and C but has only B available.
	remainingMons := len(monmap) - 1
	requiredQuorum := remainingMons/2 + 1
	remainingInQuorum := 0
	for _, name := range quorum {
		if name != hostname && monmapSet[name] {
			remainingInQuorum++
		}
	}
	if remainingInQuorum < requiredQuorum {
		return fmt.Errorf(
			"%w: refusing to remove monitor %q: %d remaining monitor(s) in quorum, need %d for the resulting %d-monitor map",
			ErrKeepOneInvariant,
			hostname,
			remainingInQuorum,
			requiredQuorum,
			remainingMons,
		)
	}

	removeErr := removeMon(ctx, hostname)
	if removeErr == nil {
		// Ceph replies to a successful `mon rm` only after the monmap proposal
		// commits. The removed MON can immediately exit, so a follow-up client
		// command from that host may be unable to reach the remaining quorum.
		return nil
	}

	absent, verifyErr := verifyMonAbsent(ctx, hostname)
	if verifyErr != nil {
		return fmt.Errorf("%w; failed to verify monitor removal: %v", removeErr, verifyErr)
	}
	if absent {
		// The command response was ambiguous, but the monmap proves that the
		// removal committed successfully.
		return nil
	}
	return removeErr
}

func getActiveMons() ([]string, error) {
	output, err := common.ProcessExec.RunCommand("ceph", "-s", "-f", "json")
	if err != nil {
		logger.Errorf("Failed fetching ceph status: %v", err)
		return nil, err
	}

	logger.Debugf("Ceph Status:\n%s", output)

	// Get the active mons services.
	activeMons := []string{}
	result := gjson.Get(output, "quorum_names")
	for _, name := range result.Array() {
		activeMons = append(activeMons, name.String())
	}

	return activeMons, nil
}
