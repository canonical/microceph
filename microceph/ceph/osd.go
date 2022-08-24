package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/canonical/microcluster/state"
	"github.com/lxc/lxd/lxd/resources"
	"github.com/lxc/lxd/shared"
	"github.com/pborman/uuid"

	"github.com/canonical/microceph/microceph/database"
)

func nextOSD() (int64, error) {
	osds, err := cephRun("osd", "ls")
	if err != nil {
		return -1, err
	}

	ids := []int64{}
	for _, line := range strings.Split(osds, "\n") {
		if line == "" {
			continue
		}

		id, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			continue
		}

		ids = append(ids, id)
	}

	nextID := int64(0)
	for {
		if !shared.Int64InSlice(nextID, ids) {
			return nextID, nil
		}

		nextID++
	}
}

func AddOSD(s *state.State, path string, wipe bool) error {
	// Validate the path.
	if !shared.IsBlockdevPath(path) {
		return fmt.Errorf("Invalid disk path: %s", path)
	}

	_, _, major, minor, _, _, err := shared.GetFileStat(path)
	if err != nil {
		return fmt.Errorf("Invalid disk path: %w", err)
	}

	dev := fmt.Sprintf("%d:%d", major, minor)

	// Lookup a stable path for it.
	storage, err := resources.GetStorage()
	if err != nil {
		return fmt.Errorf("Unable to list system disks: %w", err)
	}

	for _, disk := range storage.Disks {
		// Check if full disk.
		if disk.Device == dev {
			path = fmt.Sprintf("/dev/disk/by-id/%s", disk.DeviceID)
			break
		}

		// Check if partition.
		found := false
		for _, part := range disk.Partitions {
			if part.Device == dev {
				path = fmt.Sprintf("/dev/disk/by-id/%s-part%d", disk.DeviceID, part.Partition)
				break
			}
		}

		if found {
			break
		}
	}

	// Wipe the block device if requested.
	if wipe {
		// FIXME: Do a Go implementation.
		_, err := shared.RunCommand("dd", "if=/dev/zero", fmt.Sprintf("of=%s", path), "bs=4M", "count=10", "status=none")
		if err != nil {
			return fmt.Errorf("Failed to wipe the device: %w", err)
		}
	}

	// Get a OSD number.
	nr, err := nextOSD()
	if err != nil {
		return fmt.Errorf("Failed to find next OSD number: %w", err)
	}

	// Create directory.
	dataPath := filepath.Join(os.Getenv("SNAP_COMMON"), "data")
	osdDataPath := filepath.Join(dataPath, "osd", fmt.Sprintf("ceph-%d", nr))

	err = os.MkdirAll(osdDataPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap monitor: %w", err)
	}

	// Generate keyring.
	err = genAuth(filepath.Join(osdDataPath, "keyring"), fmt.Sprintf("osd.%d", nr), []string{"mgr", "allow profile osd"}, []string{"mon", "allow profile osd"}, []string{"osd", "allow *"})
	if err != nil {
		return fmt.Errorf("Failed to generate OSD keyring: %w", err)
	}

	// Setup device symlink.
	err = os.Symlink(path, filepath.Join(osdDataPath, "block"))
	if err != nil {
		return fmt.Errorf("Failed to add block symlink: %w", err)
	}

	// Generate OSD uuid.
	fsid := uuid.NewRandom().String()

	// Write fsid file.
	err = os.WriteFile(filepath.Join(osdDataPath, "fsid"), []byte(fsid), 0600)
	if err != nil {
		return fmt.Errorf("Failed to write fsid: %w", err)
	}

	// Bootstrap OSD.
	_, err = shared.RunCommand("ceph-osd", "--mkfs", "--no-mon-config", "-i", fmt.Sprintf("%d", nr))
	if err != nil {
		return fmt.Errorf("Failed to bootstrap OSD: %w", err)
	}

	// Write the stamp file.
	err = os.WriteFile(filepath.Join(osdDataPath, "ready"), []byte(""), 0600)
	if err != nil {
		return fmt.Errorf("Failed to write stamp file: %w", err)
	}

	// Record the disk.
	err = s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		_, err := database.CreateDisk(ctx, tx, database.Disk{Member: s.Name(), Path: path, OSD: int(nr)})
		if err != nil {
			return fmt.Errorf("Failed to record disk: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}
