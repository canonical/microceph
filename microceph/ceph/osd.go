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
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/shared"
	"github.com/pborman/uuid"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
)

func nextOSD(s *state.State) (int64, error) {
	// Get the used OSD ids from Ceph.
	osds, err := cephRun("osd", "ls")
	if err != nil {
		return -1, err
	}

	cephIds := []int64{}
	for _, line := range strings.Split(osds, "\n") {
		if line == "" {
			continue
		}

		id, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			continue
		}

		cephIds = append(cephIds, id)
	}

	// Get the used OSD ids from the database.
	dbIds := []int64{}
	err = s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		disks, err := database.GetDisks(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to fetch disks: %w", err)
		}

		for _, disk := range disks {
			dbIds = append(dbIds, int64(disk.OSD))
		}

		return nil
	})
	if err != nil {
		return -1, err
	}

	// Find next available.
	nextID := int64(0)
	for {
		if !shared.Int64InSlice(nextID, cephIds) && !shared.Int64InSlice(nextID, dbIds) {
			return nextID, nil
		}

		nextID++
	}
}

// AddOSD adds an OSD to the cluster, given a device path and a flag for wiping
func AddOSD(s *state.State, path string, wipe bool) error {
	revert := revert.New()
	defer revert.Fail()

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
			candidate := fmt.Sprintf("/dev/disk/by-id/%s", disk.DeviceID)

			// check if candidate exists
			if shared.PathExists(candidate) && !shared.IsDir(candidate) {
				path = candidate
			} else {
				candidate = fmt.Sprintf("/dev/disk/by-path/%s", disk.DevicePath)
				if shared.PathExists(candidate) && !shared.IsDir(candidate) {
					path = candidate
				}
			}

			break
		}

		// Check if partition.
		found := false
		for _, part := range disk.Partitions {
			if part.Device == dev {
				candidate := fmt.Sprintf("/dev/disk/by-id/%s-part%d", disk.DeviceID, part.Partition)
				if shared.PathExists(candidate) {
					path = candidate
				} else {
					candidate = fmt.Sprintf("/dev/disk/by-path/%s-part%d", disk.DevicePath, part.Partition)
					if shared.PathExists(candidate) {
						path = candidate
					}
				}

				break
			}
		}

		if found {
			break
		}
		// Fallthrough. We didn't find a /dev/disk path for this device, use the original path.
	}

	// Wipe the block device if requested.
	if wipe {
		// FIXME: Do a Go implementation.
		_, err := processExec.RunCommand("dd", "if=/dev/zero", fmt.Sprintf("of=%s", path), "bs=4M", "count=10", "status=none")
		if err != nil {
			return fmt.Errorf("Failed to wipe the device: %w", err)
		}
	}

	// Get a OSD number.
	nr, err := nextOSD(s)
	if err != nil {
		return fmt.Errorf("Failed to find next OSD number: %w", err)
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

	// if we fail later, make sure we free up the record

	revert.Add(func() {
		s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
			database.DeleteDisk(ctx, tx, s.Name(), path)
			return nil
		})
	})

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
	_, err = processExec.RunCommand("ceph-osd", "--mkfs", "--no-mon-config", "-i", fmt.Sprintf("%d", nr))
	if err != nil {
		return fmt.Errorf("Failed to bootstrap OSD: %w", err)
	}

	// Write the stamp file.
	err = os.WriteFile(filepath.Join(osdDataPath, "ready"), []byte(""), 0600)
	if err != nil {
		return fmt.Errorf("Failed to write stamp file: %w", err)
	}

	// Spawn the OSD.
	err = snapReload("osd")
	if err != nil {
		return err
	}

	revert.Success() // Revert functions added are not run on return.
	return nil
}

// ListOSD lists current OSD disks
func ListOSD(s *state.State) (types.Disks, error) {
	disks := types.Disks{}

	// Get the OSDs from the database.
	err := s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		records, err := database.GetDisks(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to fetch disks: %w", err)
		}

		for _, disk := range records {
			disks = append(disks, types.Disk{
				Location: disk.Member,
				OSD:      int64(disk.OSD),
				Path:     disk.Path,
			})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return disks, nil
}
