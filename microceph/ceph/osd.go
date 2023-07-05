package ceph

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/resources"
	"github.com/canonical/lxd/lxd/revert"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/microcluster/state"
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

// setupEncryptedOSD sets up an encrypted OSD on the given disk.
//
// Takes a path to the disk device as well as the osd data path and the osd id.
// Returns the path to the encrypted device and an error if any.
func setupEncryptedOSD(devicePath string, osdDataPath string, osdID int64) (string, error) {
	if err := os.Symlink(devicePath, filepath.Join(osdDataPath, "unencrypted")); err != nil {
		return "", fmt.Errorf("Failed to add unencrypted block symlink: %w", err)
	}

	// Create a key for the encrypted device
	key, err := createKey()
	if err != nil {
		return "", fmt.Errorf("Key creation error: %w", err)
	}

	// Store key in ceph key value store
	if err = storeKey(key, osdID); err != nil {
		return "", fmt.Errorf("Key store error: %w", err)
	}

	// Encrypt the device
	if err = encryptDevice(devicePath, key); err != nil {
		return "", fmt.Errorf("Failed to encrypt: %w", err)
	}

	// Open the encrypted device
	encryptedDevicePath, err := openEncryptedDevice(devicePath, osdID, key)
	if err != nil {
		return "", fmt.Errorf("Failed to open: %w", err)
	}
	return encryptedDevicePath, nil
}

// createKey creates a 128 bytes long key for use with LUKS.
func createKey() ([]byte, error) {
	// Generate a random data.
	key := make([]byte, 96)
	_, err := rand.Read(key)
	if err != nil {
		return nil, fmt.Errorf("Failed to generate random key: %w", err)
	}

	// Encode as base64, this results in 128 bytes.
	return []byte(base64.StdEncoding.EncodeToString(key)), nil
}

// encryptDevice encrypts the given device with the given key.
func encryptDevice(path string, key []byte) error {
	// Run the cryptsetup command.
	cmd := exec.Command(
		"cryptsetup",
		"--batch-mode",
		"--key-file", "-",
		"luksFormat",
		path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("Error in cryptsetup pipe: %s", err)
	}
	if _, err = stdin.Write(key); err != nil {
		return fmt.Errorf("Error writing key to cryptsetup pipe: %s", err)
	}
	stdin.Close()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Failed to luksFormat device: %s, %s, %s", path, err, out)
	}
	return nil
}

// Store the key in the ceph key value store, under a name that derives from the osd id.
func storeKey(key []byte, osdID int64) error {
	// Run the ceph config-key set command
	_, err := processExec.RunCommand("ceph", "config-key", "set", fmt.Sprintf("microceph:osd.%d/key", osdID), string(key))
	if err != nil {
		return fmt.Errorf("Failed to store key: %w", err)
	}
	return nil
}

// Open the encrypted device and return its path.
func openEncryptedDevice(path string, osdID int64, key []byte) (string, error) {
	// Run the cryptsetup open command, expect key on stdin
	cmd := exec.Command(
		"cryptsetup",
		"--keyfile-size", "128",
		"--key-file", "-",
		"luksOpen",
		path,
		fmt.Sprintf("luksosd-%d", osdID),
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("Error in cryptsetup pipe: %s", err)
	}
	if _, err = stdin.Write(key); err != nil {
		return "", fmt.Errorf("Error writing key to cryptsetup pipe: %s", err)
	}
	stdin.Close()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf(`Failed to luksOpen: %s, %s, %s

NOTE: OSD Encryption requires a snapd >= 2.59.1
Verify your version of snapd by running "snap version"
`, path, err, out)
	}
	return fmt.Sprintf("/dev/mapper/luksosd-%d", osdID), nil
}

// checkEncryptSupport checks if the kernel supports encryption.
// Checks performed:
// - Check if the kernel module is loaded.
// - Check if we have a mapper control file.
// - Check if we can access /run
func checkEncryptSupport() error {
	// Check if we have a mapper
	if _, err := os.Stat("/dev/mapper/control"); err != nil {
		return fmt.Errorf("Missing /dev/mapper/control: %w", err)
	}

	// Check if the dm-crypt interface is not connected.
	if !isIntfConnected("dm-crypt") {
		helper := fmt.Sprint("Use \"sudo snap connect microceph:dm-crypt\" to enable encryption.")
		return fmt.Errorf("dm-crypt interface connection missing: \n%s", helper)
	}

	// Check if we have the dm_crypt module
	inf, err := os.Stat("/sys/module/dm_crypt")
	if err != nil || inf == nil || !inf.IsDir() {
		return fmt.Errorf("Missing dm_crypt module: %w", err)
	}

	// Check if we can list the /run directory; older snapd had an issue with this, https://github.com/snapcore/snapd/pull/12445
	if _, err = os.ReadDir("/run"); err != nil {
		return fmt.Errorf("Can't access /run, might need to update snapd to >=2.59.1: %w", err)
	}
	return nil
}

// setHostFailureDomain sets the host failure domain for the given host.
func setHostFailureDomain() error {
	var err error

	if haveCrushRule("microceph_auto_host") {
		// Already setup up, nothing to do.
		return nil
	}
	err = addCrushRule("microceph_auto_host", "host")
	if err != nil {
		return err
	}
	osdPools, err := getPoolsForDomain("osd")
	if err != nil {
		return err
	}
	for _, pool := range osdPools {
		err = setPoolCrushRule(pool, "microceph_auto_host")
		if err != nil {
			return err
		}
	}
	return nil
}

// updateFailureDomain checks if we need to update the crush rules failure domain.
// Once we have at least 3 nodes with at least 1 OSD each, we set the failure domain to host.
// Currently this function only handles scale-up scenarios, i.e. adding a new node.
func updateFailureDomain(s *state.State) error {
	numNodes, err := database.MemberCounter.Count(s)
	if err != nil {
		return fmt.Errorf("Failed to count members: %w", err)
	}

	if numNodes >= 3 {
		err = setHostFailureDomain()
		if err != nil {
			return fmt.Errorf("Failed to set host failure domain: %w", err)
		}
		if haveCrushRule("microceph_auto_osd") {
			err := removeCrushRule("microceph_auto_osd")
			if err != nil {
				return fmt.Errorf("Failed to remove microceph_auto_osd rule: %w", err)
			}
		}
	}
	return nil
}

// AddOSD adds an OSD to the cluster, given a device path and a flag for wiping
func AddOSD(s *state.State, path string, wipe bool, encrypt bool) error {
	revert := revert.New()
	defer revert.Fail()

	// Validate the path.
	if !shared.IsBlockdevPath(path) {
		return fmt.Errorf("Invalid disk path: %s", path)
	}
	// Check if we need to support encryption
	if encrypt {
		if err := checkEncryptSupport(); err != nil {
			return fmt.Errorf("Encryption unsupported on this machine: %w", err)
		}
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

	dataPath := filepath.Join(os.Getenv("SNAP_COMMON"), "data")
	osdDataPath := filepath.Join(dataPath, "osd", fmt.Sprintf("ceph-%d", nr))

	// if we fail later, make sure we free up the record
	revert.Add(func() {
		os.RemoveAll(osdDataPath)
		s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
			database.DeleteDisk(ctx, tx, s.Name(), path)
			return nil
		})
	})

	// Create directory.
	err = os.MkdirAll(osdDataPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap monitor: %w", err)
	}

	// Generate keyring.
	err = genAuth(filepath.Join(osdDataPath, "keyring"), fmt.Sprintf("osd.%d", nr), []string{"mgr", "allow profile osd"}, []string{"mon", "allow profile osd"}, []string{"osd", "allow *"})
	if err != nil {
		return fmt.Errorf("Failed to generate OSD keyring: %w", err)
	}

	var blockPath string
	if encrypt {
		blockPath, err = setupEncryptedOSD(path, osdDataPath, nr)
		if err != nil {
			return err
		}
	} else {
		blockPath = path
	}

	// Setup device symlink.
	if err = os.Symlink(blockPath, filepath.Join(osdDataPath, "block")); err != nil {
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
	err = snapRestart("osd", true)
	if err != nil {
		return err
	}

	// Maybe update the failure domain
	err = updateFailureDomain(s)
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
