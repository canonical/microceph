package ceph

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"

	"github.com/canonical/lxd/lxd/resources"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/microcluster/state"
	"github.com/pborman/uuid"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
)

func prepareDisk(disk *types.DiskParameter, suffix string, osdPath string, osdID int64) error {
	if disk.Wipe {
		err := timeoutWipe(disk.Path)
		if err != nil {
			return fmt.Errorf("failed to wipe device %s: %w", disk.Path, err)
		}
	}
	if disk.Encrypt {
		err := checkEncryptSupport()
		if err != nil {
			return fmt.Errorf("encryption unsupported on this machine: %w", err)
		}
		path, err := setupEncryptedOSD(disk.Path, osdPath, osdID, suffix)
		if err != nil {
			return fmt.Errorf("failed to encrypt device %s: %w", disk.Path, err)
		}
		disk.Path = path
	}
	// Only the data device needs to be symlinked (suffix != "").
	// Other devices (WAL and DB) are automatically handled by Ceph itself.
	if suffix != "" {
		return nil
	}
	return os.Symlink(disk.Path, filepath.Join(osdPath, "block"))
}

// setupEncryptedOSD sets up an encrypted OSD on the given disk.
//
// Takes a path to the disk device as well as the OSD data path, the OSD id and
// a suffix (to differentiate invocations between data, WAL and DB devices).
// Returns the path to the encrypted device and an error if any.
func setupEncryptedOSD(devicePath string, osdDataPath string, osdID int64, suffix string) (string, error) {
	if err := os.Symlink(devicePath, filepath.Join(osdDataPath, "unencrypted"+suffix)); err != nil {
		return "", fmt.Errorf("failed to add unencrypted block symlink: %w", err)
	}

	// Create a key for the encrypted device
	key, err := createKey()
	if err != nil {
		return "", fmt.Errorf("key creation error: %w", err)
	}

	// Store key in ceph key value store
	if err = storeKey(key, osdID, suffix); err != nil {
		return "", fmt.Errorf("key store error: %w", err)
	}

	// Encrypt the device
	if err = encryptDevice(devicePath, key); err != nil {
		return "", fmt.Errorf("failed to encrypt: %w", err)
	}

	// Open the encrypted device
	encryptedDevicePath, err := openEncryptedDevice(devicePath, osdID, key, suffix)
	if err != nil {
		return "", fmt.Errorf("failed to open: %w", err)
	}
	return encryptedDevicePath, nil
}

// createKey creates a 128 bytes long key for use with LUKS.
func createKey() ([]byte, error) {
	// Generate a random data.
	key := make([]byte, 96)
	_, err := rand.Read(key)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random key: %w", err)
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
		return fmt.Errorf("error in cryptsetup pipe: %s", err)
	}
	if _, err = stdin.Write(key); err != nil {
		return fmt.Errorf("error writing key to cryptsetup pipe: %s", err)
	}
	stdin.Close()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to luksFormat device: %s, %s, %s", path, err, out)
	}
	return nil
}

// Store the key in the ceph key value store, under a name that derives from the osd id.
func storeKey(key []byte, osdID int64, suffix string) error {
	// Run the ceph config-key set command
	_, err := processExec.RunCommand("ceph", "config-key", "set", fmt.Sprintf("microceph:osd%s.%d/key", suffix, osdID), string(key))
	if err != nil {
		return fmt.Errorf("failed to store key: %w", err)
	}
	return nil
}

// Open the encrypted device and return its path.
func openEncryptedDevice(path string, osdID int64, key []byte, suffix string) (string, error) {
	// Run the cryptsetup open command, expect key on stdin
	cmd := exec.Command(
		"cryptsetup",
		"--keyfile-size", "128",
		"--key-file", "-",
		"luksOpen",
		path,
		fmt.Sprintf("luksosd%s-%d", suffix, osdID),
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("error in cryptsetup pipe: %s", err)
	}
	if _, err = stdin.Write(key); err != nil {
		return "", fmt.Errorf("error writing key to cryptsetup pipe: %s", err)
	}
	stdin.Close()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf(`failed to luksOpen: %s, %s, %s

NOTE: OSD Encryption requires a snapd >= 2.59.1
Verify your version of snapd by running "snap version"
`, path, err, out)
	}
	return fmt.Sprintf("/dev/mapper/luksosd%s-%d", suffix, osdID), nil
}

// checkEncryptSupport checks if the kernel supports encryption.
// Checks performed:
// - Check if the kernel module is loaded.
// - Check if we have a mapper control file.
// - Check if we can access /run
func checkEncryptSupport() error {
	// Check if we have a mapper
	if _, err := os.Stat("/dev/mapper/control"); err != nil {
		return fmt.Errorf("missing /dev/mapper/control: %w", err)
	}

	// Check if the dm-crypt interface is not connected.
	if !isIntfConnected("dm-crypt") {
		helper := "use \"sudo snap connect microceph:dm-crypt ; sudo snap restart microceph.daemon\" to enable encryption."
		return fmt.Errorf("dm-crypt interface connection missing: \n%s", helper)
	}

	// Check if we have the dm_crypt module
	inf, err := os.Stat("/sys/module/dm_crypt")
	if err != nil || inf == nil || !inf.IsDir() {
		return fmt.Errorf("missing dm_crypt module: %w", err)
	}

	// Check if we can list the /run directory; older snapd had an issue with this, https://github.com/snapcore/snapd/pull/12445
	if _, err = os.ReadDir("/run"); err != nil {
		return fmt.Errorf("can't access /run, might need to update snapd to >=2.59.1: %w", err)
	}
	return nil
}

// switchFailureDomain switches the crush rules failure domain from old to new
func switchFailureDomain(old string, new string) error {
	var err error

	newRule := fmt.Sprintf("microceph_auto_%s", new)
	logger.Debugf("Setting default crush rule to %v", newRule)
	err = setDefaultCrushRule(newRule)
	if err != nil {
		return err
	}

	osdPools, err := getPoolsForDomain(old)
	logger.Debugf("Found pools %v for domain %v", osdPools, old)
	if err != nil {
		return err
	}
	for _, pool := range osdPools {
		logger.Debugf("Setting pool %v crush rule to %v", pool, newRule)
		err = setPoolCrushRule(pool, newRule)
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
		return fmt.Errorf("failed to count members: %w", err)
	}

	if numNodes >= 3 {
		err = switchFailureDomain("osd", "host")
		if err != nil {
			return fmt.Errorf("failed to set host failure domain: %w", err)
		}
	}
	return nil
}

func setStablePath(storage *api.ResourcesStorage, param *types.DiskParameter) error {
	// Validate the path.
	if !shared.IsBlockdevPath(param.Path) {
		return fmt.Errorf("invalid disk path: %s", param.Path)
	}

	_, _, major, minor, _, _, err := shared.GetFileStat(param.Path)
	if err != nil {
		return fmt.Errorf("invalid disk path: %w", err)
	}

	dev := fmt.Sprintf("%d:%d", major, minor)

	for _, disk := range storage.Disks {
		// Check if full disk.
		if disk.Device == dev {
			candidate := fmt.Sprintf("/dev/disk/by-id/%s", disk.DeviceID)

			// check if candidate exists
			if shared.PathExists(candidate) && !shared.IsDir(candidate) {
				param.Path = candidate
			} else {
				candidate = fmt.Sprintf("/dev/disk/by-path/%s", disk.DevicePath)
				if shared.PathExists(candidate) && !shared.IsDir(candidate) {
					param.Path = candidate
				}
			}

			break
		}

		// Check if partition.
		for _, part := range disk.Partitions {
			if part.Device == dev {
				candidate := fmt.Sprintf("/dev/disk/by-id/%s-part%d", disk.DeviceID, part.Partition)
				if shared.PathExists(candidate) {
					param.Path = candidate
				} else {
					candidate = fmt.Sprintf("/dev/disk/by-path/%s-part%d", disk.DevicePath, part.Partition)
					if shared.PathExists(candidate) {
						param.Path = candidate
					}
				}

				break
			}
		}
	}

	return nil
}

// parseBackingSpec parses a loopback file specification.
// The specification is of the form "loop,<size><unit>,<number>".
// The function returns the size in MB and the number of disks.
func parseBackingSpec(spec string) (uint64, int, error) {
	r := regexp.MustCompile("loop,([1-9][0-9]*[MGT]),([1-9][0-9]*)")

	match := r.FindStringSubmatch(spec)
	if match == nil {
		return 0, 0, fmt.Errorf("illegal spec: %s", spec)
	}
	// Parse the size and unit from the first matched group.
	sizeStr := match[1][:len(match[1])-1]
	unit := match[1][len(match[1])-1:]

	size, err := strconv.ParseUint(sizeStr, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse size from spec %s: %w", spec, err)
	}

	// Convert the size to MB.
	switch strings.ToUpper(unit) {
	case "G":
		size *= 1024
	case "T":
		size *= 1024 * 1024
	}

	num, err := strconv.Atoi(match[2])
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse number disks from spec %s: %w", spec, err)
	}

	return size, num, nil
}

// getFreeSpace returns the number of free megabytes of disk capacity
// available at the given path.
func getFreeSpace(path string) (uint64, error) {
	var stat syscall.Statfs_t

	// Perform a system call to get file system statistics.
	err := syscall.Statfs(path, &stat)
	if err != nil {
		return 0, err
	}

	// Calculate free space in bytes and convert to megabytes.
	// stat.Bavail gives free blocks available to a non-superuser.
	// stat.Bsize gives the size of each block in bytes.
	freeSpace := stat.Bavail * uint64(stat.Bsize) / 1024 / 1024

	return freeSpace, nil
}

// createBackingFile creates a backing file of the given size in MB
// and returns the file name.
func createBackingFile(dir string, size uint64) (string, error) {
	backing := filepath.Join(dir, "osd-backing.img")
	_, err := processExec.RunCommand("truncate", "-s", fmt.Sprintf("%dM", size), backing)
	if err != nil {
		return "", fmt.Errorf("failed to create backing file %s: %w", backing, err)
	}
	return backing, nil
}

// AddLoopBackOSDs adds OSDs to the cluster backed by loopback files
func AddLoopBackOSDs(s *state.State, spec string) error {
	size, num, err := parseBackingSpec(spec)
	if err != nil {
		return err
	}
	// check available capacity for backing files under $SNAP_COMMON
	freeSpace, err := getFreeSpace(os.Getenv("SNAP_COMMON"))
	if err != nil {
		return err
	}
	if freeSpace < size*uint64(num) {
		return fmt.Errorf("insufficient free space for %d loopback files of size %dMB", num, size)
	}
	// create backing files in a loop and add them to the cluster
	for i := 0; i < num; i++ {
		err = AddOSD(s, types.DiskParameter{LoopSize: size}, nil, nil)
		if err != nil {
			return fmt.Errorf("failed to add loop OSD: %w", err)
		}
	}
	return nil
}

// bootstrapOSD bootstraps an OSD.
func bootstrapOSD(osdDataPath string, nr int64, wal, db *types.DiskParameter, storage *api.ResourcesStorage) error {
	var err error

	args := []string{"--mkfs", "--no-mon-config", "-i", fmt.Sprintf("%d", nr)}
	if wal != nil {
		if err = setStablePath(storage, wal); err != nil {
			return fmt.Errorf("failed to set stable path for WAL: %w", err)
		}

		err = prepareDisk(wal, ".wal", osdDataPath, nr)
		if err != nil {
			return fmt.Errorf("failed to set up WAL device: %w", err)
		}
		args = append(args, []string{"--bluestore-block-wal-path", wal.Path}...)
	}
	if db != nil {
		if err = setStablePath(storage, db); err != nil {
			return fmt.Errorf("failed to set stable path for DB: %w", err)
		}

		err = prepareDisk(db, ".db", osdDataPath, nr)
		if err != nil {
			return fmt.Errorf("failed to set up DB device: %w", err)
		}
		args = append(args, []string{"--bluestore-block-db-path", db.Path}...)
	}

	_, err = processExec.RunCommand("ceph-osd", args...)
	if err != nil {
		return fmt.Errorf("failed to bootstrap OSD: %w", err)
	}

	// Write the stamp file.
	err = os.WriteFile(filepath.Join(osdDataPath, "ready"), []byte(""), 0600)
	if err != nil {
		return fmt.Errorf("failed to write stamp file: %w", err)
	}
	return nil
}

func validateBulkDiskAdditionArgs(disks []types.DiskParameter, wal *types.DiskParameter, db *types.DiskParameter) error {
	// No validation for non-batch requests.
	if len(disks) == 1 {
		return nil
	}

	// check if wal/db devices are provided for batch request.
	if wal != nil || db != nil {
		err := fmt.Errorf("wal/db devices are not supported in batch disk addition")
		logger.Error(err.Error())
		return err
	}

	// check if loop spec is provided in batch request arguments.
	for _, disk := range disks {
		if strings.HasPrefix(disk.Path, constants.LoopSpecId) {
			err := fmt.Errorf("cannot add loop spec '%s', add a single loop spec or one or more block device paths", disk.Path)
			logger.Error(err.Error())
			return err
		}
	}

	return nil
}

// prepareValidationFailureResp generates the failure response for argument validation errors.
func prepareValidationFailureResp(disks []types.DiskParameter, err error) types.DiskAddResponse {
	ret := types.DiskAddResponse{ValidationError: err.Error()}

	for _, disk := range disks {
		// Only append this error for the first disk since
		ret.Reports = append(ret.Reports, types.DiskAddReport{Path: disk.Path, Report: "Failure", Error: ""})
	}

	return ret
}

// AddBulkDisks adds multiple disks as OSDs and generates the API response for request.
func AddBulkDisks(s *state.State, disks []types.DiskParameter, wal *types.DiskParameter, db *types.DiskParameter) types.DiskAddResponse {
	ret := types.DiskAddResponse{}

	if len(disks) == 1 {
		// Add single disk with requested WAL/DB devices.
		resp := AddSingleDisk(s, disks[0], wal, db)
		ret.Reports = append(ret.Reports, resp)
		ret.ValidationError = "" // Validation is done for batch requests.
		return ret
	}

	// validate Arguments for batch request.
	err := validateBulkDiskAdditionArgs(disks, wal, db)
	if err != nil {
		// Disk addition is skipped if validation errors are found.
		return prepareValidationFailureResp(disks, err)
	} else {
		ret.ValidationError = ""
	}

	// Add all requested disks.
	for _, disk := range disks {
		resp := AddSingleDisk(s, disk, nil, nil)
		ret.Reports = append(ret.Reports, resp)
	}

	return ret
}

// AddSingleDisk is a wrapper around AddOSD which logs disk addition failures and returns a formatted response.
func AddSingleDisk(s *state.State, disk types.DiskParameter, wal *types.DiskParameter, db *types.DiskParameter) types.DiskAddReport {
	if strings.Contains(disk.Path, constants.LoopSpecId) {
		// Add file based OSDs.
		err := AddLoopBackOSDs(s, disk.Path)
		if err != nil {
			logger.Errorf("failed to add disk: spec %s, err %v", disk.Path, err)
			return types.DiskAddReport{Path: disk.Path, Report: "Failure", Error: err.Error()}
		}
	} else {
		// Add physical disk based OSD.
		err := AddOSD(s, disk, wal, db)
		if err != nil {
			logger.Errorf("failed to add disk: path %s, err %v", disk.Path, err)
			// return failure as response.
			return types.DiskAddReport{Path: disk.Path, Report: "Failure", Error: err.Error()}
		}
	}

	// return success as response.
	return types.DiskAddReport{Path: disk.Path, Report: "Success", Error: ""}
}

// AddOSD adds an OSD to the cluster, given the data, WAL and DB devices and their respective
// flags for wiping and encrypting.
func AddOSD(s *state.State, data types.DiskParameter, wal *types.DiskParameter, db *types.DiskParameter) error {
	logger.Debugf("Adding OSD %s", data.Path)

	var err error

	// sanity: loopback file and WAL/DB are mutually exclusive
	if data.LoopSize != 0 && (wal != nil || db != nil) {
		return fmt.Errorf("loopback and WAL/DB are mutually exclusive")
	}

	revert := revert.New()
	defer revert.Fail()

	var storage *api.ResourcesStorage

	if data.LoopSize == 0 {
		// We have a physical device.
		// Lookup a stable path for it.
		storage, err = resources.GetStorage()
		if err != nil {
			return fmt.Errorf("unable to list system disks: %w", err)
		}
		if err := setStablePath(storage, &data); err != nil {
			return fmt.Errorf("failed to set stable disk path: %w", err)
		}
	}

	// Record the disk.
	var nr int64
	err = s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		nr, err = database.CreateDisk(ctx, tx, database.Disk{Member: s.Name(), Path: data.Path})
		if err != nil {
			return fmt.Errorf("failed to record disk: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	logger.Debugf("Created disk record for osd.%d", nr)

	osdDataPath := filepath.Join(constants.GetPathConst().DataPath, "osd", fmt.Sprintf("ceph-%d", nr))

	// if we fail later, make sure we free up the record
	revert.Add(func() {
		os.RemoveAll(osdDataPath)
		s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
			database.DeleteDisk(ctx, tx, s.Name(), data.Path)
			return nil
		})
	})

	// Create directory.
	err = os.MkdirAll(osdDataPath, 0700)
	if err != nil {
		return fmt.Errorf("failed to create OSD directory: %w", err)
	}

	// do we have a loopback file request?
	if data.LoopSize != 0 {
		backing, err := createBackingFile(osdDataPath, data.LoopSize)
		if err != nil {
			return err
		}
		data.Path = backing
		// update db, it didn't have a path before
		err = s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
			err = database.OSDQuery.UpdatePath(s, nr, backing)
			if err != nil {
				return fmt.Errorf("failed to update disk record: %w", err)
			}
			return nil
		})
	}

	// Wipe and/or encrypt the disk if needed.
	err = prepareDisk(&data, "", osdDataPath, nr)
	if err != nil {
		return fmt.Errorf("failed to prepare data device: %w", err)
	}

	// Generate keyring.
	err = genAuth(filepath.Join(osdDataPath, "keyring"), fmt.Sprintf("osd.%d", nr), []string{"mgr", "allow profile osd"}, []string{"mon", "allow profile osd"}, []string{"osd", "allow *"})
	if err != nil {
		return fmt.Errorf("failed to generate OSD keyring: %w", err)
	}

	// Generate OSD uuid.
	fsid := uuid.NewRandom().String()

	// Write fsid file.
	err = os.WriteFile(filepath.Join(osdDataPath, "fsid"), []byte(fsid), 0600)
	if err != nil {
		return fmt.Errorf("failed to write fsid: %w", err)
	}

	// Bootstrap OSD.
	err = bootstrapOSD(osdDataPath, nr, wal, db, storage)
	if err != nil {
		return err
	}

	// Spawn the OSD.
	logger.Debugf("Spawning OSD %d", nr)
	err = snapRestart("osd", true)
	if err != nil {
		return fmt.Errorf("failed to start osd.%d: %w", nr, err)
	}

	// Maybe update the failure domain
	err = updateFailureDomain(s)
	if err != nil {
		return err
	}

	revert.Success() // Revert functions added are not run on return.
	logger.Debugf("Added osd.%d", nr)
	return nil
}

// ListOSD lists current OSD disks
func ListOSD(s *state.State) (types.Disks, error) {
	return database.OSDQuery.List(s)
}

// RemoveOSD removes an OSD disk
func RemoveOSD(s interfaces.StateInterface, osd int64, bypassSafety bool, timeout int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()
	err := doRemoveOSD(ctx, s, osd, bypassSafety)
	if err != nil {
		// Checking if the error is a context deadline exceeded error
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("timeout (%ds) reached while removing osd.%d, abort", timeout, osd)
		}
		return err
	}
	return nil

}

// sanityCheck checks if input is valid
func sanityCheck(s interfaces.StateInterface, osd int64) error {
	// check osd is positive
	if osd < 0 {
		return fmt.Errorf("OSD must be a positive integer")
	}

	// check if the OSD exists in the database
	exists, err := database.OSDQuery.HaveOSD(s.ClusterState(), osd)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("osd.%d not found", osd)
	}
	return nil
}

// IsDowngradeNeeded checks if we need to downgrade the failure domain from 'host' to 'osd' level
// if we remove the given OSD
func IsDowngradeNeeded(s interfaces.StateInterface, osd int64) (bool, error) {
	currentRule, err := getDefaultCrushRule()
	if err != nil {
		return false, err
	}
	hostRule, err := getCrushRuleID("microceph_auto_host")
	if err != nil {
		return false, err
	}
	if currentRule != hostRule {
		// either we're at 'osd' level or we're using a custom rule
		// in both cases we won't downgrade
		logger.Infof("No need to downgrade auto failure domain, current rule is %v", currentRule)
		return false, nil
	}
	numNodes, err := database.MemberCounter.CountExclude(s.ClusterState(), osd)
	logger.Infof("Number of nodes excluding osd.%v: %v", osd, numNodes)
	if err != nil {
		return false, err
	}
	if numNodes < 3 { // need to scale down
		return true, nil
	}
	return false, nil
}

// scaleDownFailureDomain scales down the failure domain from 'host' to 'osd' level
func scaleDownFailureDomain(s interfaces.StateInterface, osd int64) error {
	needDowngrade, err := IsDowngradeNeeded(s, osd)
	logger.Debugf("Downgrade needed: %v", needDowngrade)
	if err != nil {
		return err
	}
	if !needDowngrade {
		return nil
	}
	err = switchFailureDomain("host", "osd")
	if err != nil {
		return fmt.Errorf("failed to switch failure domain: %w", err)
	}
	return nil
}

// reweightOSD reweights the given OSD to the given weight
func reweightOSD(ctx context.Context, osd int64, weight float64) {
	logger.Debugf("Reweighting osd.%d to %f", osd, weight)
	_, err := processExec.RunCommand(
		"ceph", "osd", "crush", "reweight",
		fmt.Sprintf("osd.%d", osd),
		fmt.Sprintf("%f", weight),
	)
	if err != nil {
		// only log a warn, don't treat fail to reweight as a fatal error
		logger.Warnf("Failed to reweight osd.%d: %v", osd, err)
	}
}

func doPurge(osd int64) error {
	// run ceph osd purge command
	_, err := processExec.RunCommand(
		"ceph", "osd", "purge", fmt.Sprintf("osd.%d", osd),
		"--yes-i-really-mean-it",
	)
	return err
}

func purgeOSD(osd int64) error {
	var err error
	retries := 10
	var backoff time.Duration

	for i := 0; i < retries; i++ {
		err = doPurge(osd)
		if err == nil {
			// Success: break the retry loop
			break
		}
		// we're getting a RunError from processExec.RunCommand, and it
		// wraps the original exit error if there's one
		exitError, ok := err.(shared.RunError).Unwrap().(*exec.ExitError)
		if !ok {
			// not an exit error, abort and bubble up the error
			logger.Warnf("Purge failed with non-exit error: %v", err)
			break
		}
		if syscall.Errno(exitError.ExitCode()) != syscall.EBUSY {
			// not a busy error, abort and bubble up the error
			logger.Warnf("Purge failed with unexpected exit error: %v", exitError)
			break
		}
		// purge failed with EBUSY - retry after a delay, and make delay exponential
		logger.Infof("Purge failed %v, retrying in %v", err, backoff)
		backoff = time.Duration(math.Pow(2, float64(i))) * time.Millisecond * 100
		time.Sleep(backoff)
	}

	if err != nil {
		logger.Errorf("Failed to purge osd.%d: %v", osd, err)
		return fmt.Errorf("failed to purge osd.%d: %w", osd, err)
	}
	logger.Infof("osd.%d purged", osd)
	return nil
}

func wipeDevice(s interfaces.StateInterface, path string) {
	var err error
	// wipe the device, retry with exponential backoff
	retries := 8
	var backoff time.Duration
	for i := 0; i < retries; i++ {
		err = timeoutWipe(path)
		if err == nil {
			// Success: break the retry loop
			break
		}
		// wipe failed - retry after a delay, and make delay exponential
		logger.Infof("Wipe failed %v, retrying in %v", err, backoff)
		backoff = time.Duration(math.Pow(2, float64(i))) * time.Millisecond * 100
		time.Sleep(backoff)
	}
	if err != nil {
		// log a warning, but don't treat wipe failure as a fatal error
		// e.g. if the device is broken, we still want to remove it from the cluster
		logger.Warnf("Fault during device wipe: %v", err)
	}
}

// timeoutWipe wipes the given device with a timeout, in order not to hang on broken disks
func timeoutWipe(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := processExec.RunCommandContext(
		ctx,
		"dd", "if=/dev/zero",
		fmt.Sprintf("of=%s", path),
		"bs=4M", "count=10", "status=none",
	)
	return err
}

func doRemoveOSD(ctx context.Context, s interfaces.StateInterface, osd int64, bypassSafety bool) error {
	var err error

	// general sanity
	err = sanityCheck(s, osd)
	if err != nil {
		return err
	}

	if !bypassSafety {
		// check: at least 3 OSDs
		err = checkMinOSDs(s, osd)
		if err != nil {
			return err
		}
	}

	err = scaleDownFailureDomain(s, osd)
	if err != nil {
		return err
	}

	// check if the osd is still in the cluster -- if we're being re-run, it might not be
	isPresent, err := haveOSDInCeph(osd)
	if err != nil {
		return fmt.Errorf("failed to check if osd.%d is present in Ceph: %w", osd, err)
	}
	// reweight/drain data
	if isPresent {
		reweightOSD(ctx, osd, 0)
	}
	// perform safety check for stopping
	if isPresent && !bypassSafety {
		err = safetyCheckStop(osd)
		if err != nil {
			return err
		}
	}
	// take the OSD out and down
	if isPresent {
		err = outDownOSD(osd)
		if err != nil {
			return err
		}
	}
	// stop the OSD service, but don't fail if it's not running
	if isPresent {
		_ = killOSD(osd)
	}
	// perform safety check for destroying
	if isPresent && !bypassSafety {
		err = safetyCheckDestroy(osd)
		if err != nil {
			return err
		}
	}
	// purge the OSD
	if isPresent {
		err = purgeOSD(osd)
		if err != nil {
			return err
		}
	}

	err = clearStorage(s, osd)
	if err != nil {
		// log error but don't fail, we still want to remove the OSD from the cluster
		logger.Errorf("Failed to clear storage for osd.%d: %v", osd, err)
	}

	// Remove osd config
	err = removeOSDConfig(osd)
	if err != nil {
		return err
	}
	// Remove db entry
	err = database.OSDQuery.Delete(s.ClusterState(), osd)
	if err != nil {
		logger.Errorf("Failed to remove osd.%d from database: %v", osd, err)
		return fmt.Errorf("failed to remove osd.%d from database: %w", osd, err)
	}
	return nil
}

func clearStorage(s interfaces.StateInterface, osd int64) error {
	path, err := database.OSDQuery.Path(s.ClusterState(), osd)
	if err != nil {
		return err
	}
	fileInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	// Typically we'll be dealing with a symlink, but lets check for safety
	if fileInfo.Mode()&os.ModeSymlink != 0 {
		fileInfo, err = os.Stat(path) // Follow the symlink
		if err != nil {
			return err
		}
	}
	if fileInfo.Mode()&os.ModeDevice != 0 {
		// wipe the device
		wipeDevice(s, path)
	}
	// backing files etc. are being removed later along with config
	return nil
}

func checkMinOSDs(s interfaces.StateInterface, osd int64) error {
	// check if we have at least 3 OSDs post-removal
	disks, err := database.OSDQuery.List(s.ClusterState())
	if err != nil {
		return err
	}
	if len(disks) <= 3 {
		return fmt.Errorf("cannot remove osd.%d we need at least 3 OSDs, have %d", osd, len(disks))
	}
	return nil
}

func outDownOSD(osd int64) error {
	_, err := processExec.RunCommand("ceph", "osd", "out", fmt.Sprintf("osd.%d", osd))
	if err != nil {
		logger.Errorf("Failed to take osd.%d out: %v", osd, err)
		return fmt.Errorf("failed to take osd.%d out: %w", osd, err)
	}
	_, err = processExec.RunCommand("ceph", "osd", "down", fmt.Sprintf("osd.%d", osd))
	if err != nil {
		logger.Errorf("Failed to take osd.%d down: %v", osd, err)
		return fmt.Errorf("failed to take osd.%d down: %w", osd, err)
	}
	return nil
}

func safetyCheckStop(osd int64) error {
	var safeStop bool

	retries := 16
	var backoff time.Duration

	for i := 0; i < retries; i++ {
		safeStop = testSafeStop(osd)
		if safeStop {
			// Success: break the retry loop
			break
		}
		backoff = time.Duration(math.Pow(2, float64(i))) * time.Millisecond * 100
		logger.Infof("osd.%d not ok to stop, retrying in %v", osd, backoff)
		time.Sleep(backoff)
	}
	if !safeStop {
		logger.Errorf("osd.%d failed to reach ok-to-stop", osd)
		return fmt.Errorf("osd.%d failed to reach ok-to-stop", osd)
	}
	logger.Infof("osd.%d ok to stop", osd)
	return nil
}

func safetyCheckDestroy(osd int64) error {
	var safeDestroy bool

	retries := 16
	var backoff time.Duration

	for i := 0; i < retries; i++ {
		safeDestroy = testSafeDestroy(osd)
		if safeDestroy {
			// Success: break the retry loop
			break
		}
		backoff = time.Duration(math.Pow(2, float64(i))) * time.Millisecond * 100
		logger.Infof("osd.%d not safe to destroy, retrying in %v", osd, backoff)
		time.Sleep(backoff)
	}
	if !safeDestroy {
		logger.Errorf("osd.%d failed to reach safe-to-destroy", osd)
		return fmt.Errorf("osd.%d failed to reach safe-to-destroy", osd)
	}
	logger.Infof("osd.%d safe to destroy", osd)
	return nil
}

func testSafeDestroy(osd int64) bool {
	// run ceph osd safe-to-destroy
	_, err := processExec.RunCommand("ceph", "osd", "safe-to-destroy", fmt.Sprintf("osd.%d", osd))
	return err == nil
}

func testSafeStop(osd int64) bool {
	// run ceph osd ok-to-stop
	_, err := processExec.RunCommand("ceph", "osd", "ok-to-stop", fmt.Sprintf("osd.%d", osd))
	return err == nil
}

func removeOSDConfig(osd int64) error {
	dataPath := filepath.Join(os.Getenv("SNAP_COMMON"), "data")
	osdDataPath := filepath.Join(dataPath, "osd", fmt.Sprintf("ceph-%d", osd))
	err := os.RemoveAll(osdDataPath)
	if err != nil {
		logger.Errorf("Failed to remove osd.%d config: %v", osd, err)
		return fmt.Errorf("failed to remove osd.%d config: %w", osd, err)
	}
	return nil
}

type Node struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type JSONData struct {
	Nodes []Node `json:"nodes"`
}

// haveOSDInCeph checks if the given OSD is present in the ceph cluster
func haveOSDInCeph(osd int64) (bool, error) {
	// run ceph osd tree
	out, err := processExec.RunCommand("ceph", "osd", "tree", "-f", "json")
	if err != nil {
		logger.Errorf("Failed to get ceph osd tree: %v", err)
		return false, fmt.Errorf("failed to get ceph osd tree: %w", err)
	}
	// parse the json output
	var tree JSONData
	err = json.Unmarshal([]byte(out), &tree)
	if err != nil {
		logger.Errorf("Failed to parse ceph osd tree: %v", err)
		return false, fmt.Errorf("failed to parse ceph osd tree: %w", err)
	}
	// query the tree for the given OSD
	for _, node := range tree.Nodes {
		if node.Type == "osd" && node.ID == osd {
			return true, nil
		}
	}
	return false, nil
}

// killOSD terminates the osd process for an osd.id
func killOSD(osd int64) error {
	cmdline := fmt.Sprintf("ceph-osd .* --id %d$", osd)
	_, err := processExec.RunCommand("pkill", "-f", cmdline)
	if err != nil {
		logger.Errorf("Failed to kill osd.%d: %v", osd, err)
		return fmt.Errorf("failed to kill osd.%d: %w", osd, err)
	}
	return nil
}

func SetReplicationFactor(pools []string, size int64) error {
	ssize := fmt.Sprintf("%d", size)
	_, err := processExec.RunCommand("ceph", "config", "set", "global",
		"osd_pool_default_size", ssize)
	if err != nil {
		return fmt.Errorf("failed to set pool size default: %w", err)
	}

	allowSizeOne := "true"
	if size != 1 {
		allowSizeOne = "false"
	}

	_, err = processExec.RunCommand("ceph", "config", "set", "global",
		"mon_allow_pool_size_one", allowSizeOne)
	if err != nil {
		return fmt.Errorf("failed to set size one pool config option: %w", err)
	}

	// This only silences a warning and should thus not return an
	// error on failure
	_, _ = processExec.RunCommand("ceph", "health", "mute", "POOL_NO_REDUNDANCY")

	if len(pools) == 1 && pools[0] == "*" {
		// Apply setting to all existing pools.
		out, err := processExec.RunCommand("ceph", "osd", "pool", "ls")
		if err != nil {
			return fmt.Errorf("failed to list pools: %w", err)
		}

		pools = strings.Split(out, "\n")
	}

	for _, pool := range pools {
		pool = strings.TrimSpace(pool)
		if pool == "" {
			continue
		}
		_, err := processExec.RunCommand("ceph", "osd", "pool", "set", pool, "size", ssize, "--yes-i-really-mean-it")
		if err != nil {
			return fmt.Errorf("failed to set pool size for %s: %w", pool, err)
		}
	}

	return nil
}

func ListPools() []string {
	args := []string{"osd", "lspools"}

	output, err := processExec.RunCommand("ceph", args...)
	if err != nil {
		return []string{}
	}

	ret := []string{}
	err = json.Unmarshal([]byte(output), &ret)
	if err != nil {
		return []string{}
	}

	return ret
}
