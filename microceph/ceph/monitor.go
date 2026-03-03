package ceph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	args := []string{
		"--create",
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

// removeMon removes a monitor from the cluster.
func removeMon(hostname string) error {
	_, err := cephRun("mon", "rm", hostname)
	if err != nil {
		logger.Errorf("failed to remove monitor %q: %v", hostname, err)
		return fmt.Errorf("failed to remove monitor %q: %w", hostname, err)
	}
	return nil
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

// WaitForCephReady polls "ceph -s" until it succeeds, indicating that
// the Ceph can accept commands.
// It retries every second until success or the context is cancelled/expired.
func WaitForCephReady(ctx context.Context) error {
	for {
		_, err := cephRunContext(ctx, "-s")
		if err == nil {
			return nil
		}

		logger.Debugf("Ceph not ready yet: %v", err)

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for Ceph to become ready: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

// WaitForOSDsReady polls until enough OSDs are up to satisfy pool replication
// requirements. The required count is max(pool.Size) across all pools, falling
// back to osd_pool_default_size if no pools exist.
func WaitForOSDsReady(ctx context.Context) error {
	required, err := getRequiredOSDCount(ctx)
	if err != nil {
		return fmt.Errorf("failed to determine required OSD count: %w", err)
	}

	for {
		count, err := getUpOSDCount(ctx)
		if err == nil && count >= required {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for OSDs to be ready: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

// getRequiredOSDCount returns the maximum pool size across all pools, or the
// default pool size if no pools exist.
func getRequiredOSDCount(ctx context.Context) (int64, error) {
	pools, err := GetOSDPools(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get pools: %w", err)
	}

	if len(pools) == 0 {
		return getDefaultPoolSize(ctx)
	}

	var maxSize int64
	for _, pool := range pools {
		if pool.Size > maxSize {
			maxSize = pool.Size
		}
	}

	return maxSize, nil
}

// getDefaultPoolSize reads the osd_pool_default_size configuration value.
func getDefaultPoolSize(ctx context.Context) (int64, error) {
	output, err := cephRunContext(ctx, "config", "get", "mon", "osd_pool_default_size")
	if err != nil {
		return 0, fmt.Errorf("failed to get osd_pool_default_size: %w", err)
	}

	size, err := strconv.ParseInt(strings.TrimSpace(output), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse osd_pool_default_size %q: %w", output, err)
	}

	return size, nil
}

// getUpOSDCount returns the number of OSDs currently in the up state.
func getUpOSDCount(ctx context.Context) (int64, error) {
	output, err := cephRunContext(ctx, "osd", "dump", "-f", "json-pretty")
	if err != nil {
		return 0, fmt.Errorf("failed to get OSD dump: %w", err)
	}

	upOsds := gjson.Get(output, "osds.#(up==1)#.uuid") // select all uuids where the up field equals 1
	return int64(len(upOsds.Array())), nil
}
