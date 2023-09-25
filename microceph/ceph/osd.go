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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/common"

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

func prepareDisk(disk *types.DiskParameter, suffix string, osdPath string, osdID int64) error {
	if disk.Wipe {
		err := timeoutWipe(disk.Path)
		if err != nil {
			return fmt.Errorf("Failed to wipe device %s: %w", disk.Path, err)
		}
	}
	if disk.Encrypt {
		err := checkEncryptSupport()
		if err != nil {
			return fmt.Errorf("Encryption unsupported on this machine: %w", err)
		}
		path, err := setupEncryptedOSD(disk.Path, osdPath, osdID, suffix)
		if err != nil {
			return fmt.Errorf("Failed to encrypt device %s: %w", disk.Path, err)
		}
		disk.Path = path
	}
	return os.Symlink(disk.Path, filepath.Join(osdPath, "block"+suffix))
}

// setupEncryptedOSD sets up an encrypted OSD on the given disk.
//
// Takes a path to the disk device as well as the OSD data path, the OSD id and
// a suffix (to differentiate invocations between data, WAL and DB devices).
// Returns the path to the encrypted device and an error if any.
func setupEncryptedOSD(devicePath string, osdDataPath string, osdID int64, suffix string) (string, error) {
	if err := os.Symlink(devicePath, filepath.Join(osdDataPath, "unencrypted"+suffix)); err != nil {
		return "", fmt.Errorf("Failed to add unencrypted block symlink: %w", err)
	}

	// Create a key for the encrypted device
	key, err := createKey()
	if err != nil {
		return "", fmt.Errorf("Key creation error: %w", err)
	}

	// Store key in ceph key value store
	if err = storeKey(key, osdID, suffix); err != nil {
		return "", fmt.Errorf("Key store error: %w", err)
	}

	// Encrypt the device
	if err = encryptDevice(devicePath, key); err != nil {
		return "", fmt.Errorf("Failed to encrypt: %w", err)
	}

	// Open the encrypted device
	encryptedDevicePath, err := openEncryptedDevice(devicePath, osdID, key, suffix)
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
func storeKey(key []byte, osdID int64, suffix string) error {
	// Run the ceph config-key set command
	_, err := processExec.RunCommand("ceph", "config-key", "set", fmt.Sprintf("microceph:osd%s.%d/key", suffix, osdID), string(key))
	if err != nil {
		return fmt.Errorf("Failed to store key: %w", err)
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
		return fmt.Errorf("Missing /dev/mapper/control: %w", err)
	}

	// Check if the dm-crypt interface is not connected.
	if !isIntfConnected("dm-crypt") {
		helper := fmt.Sprint("Use \"sudo snap connect microceph:dm-crypt ; sudo snap restart microceph.daemon\" to enable encryption.")
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
		return fmt.Errorf("Failed to count members: %w", err)
	}

	if numNodes >= 3 {
		err = switchFailureDomain("osd", "host")
		if err != nil {
			return fmt.Errorf("Failed to set host failure domain: %w", err)
		}
	}
	return nil
}

// AddOSD adds an OSD to the cluster, given a device path and a flag for wiping
func AddOSD(s *state.State, data types.DiskParameter, wal *types.DiskParameter, db *types.DiskParameter) error {
	logger.Debugf("Adding OSD %s", data.Path)

	revert := revert.New()
	defer revert.Fail()

	// Validate the path.
	if !shared.IsBlockdevPath(data.Path) {
		return fmt.Errorf("Invalid disk path: %s", data.Path)
	}

	_, _, major, minor, _, _, err := shared.GetFileStat(data.Path)
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
				data.Path = candidate
			} else {
				candidate = fmt.Sprintf("/dev/disk/by-path/%s", disk.DevicePath)
				if shared.PathExists(candidate) && !shared.IsDir(candidate) {
					data.Path = candidate
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
					data.Path = candidate
				} else {
					candidate = fmt.Sprintf("/dev/disk/by-path/%s-part%d", disk.DevicePath, part.Partition)
					if shared.PathExists(candidate) {
						data.Path = candidate
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

	// Get a OSD number.
	nr, err := nextOSD(s)
	if err != nil {
		return fmt.Errorf("Failed to find next OSD number: %w", err)
	}
	logger.Debugf("nextOSD number is %d for disk %s", nr, data.Path)

	// Record the disk.
	err = s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		_, err := database.CreateDisk(ctx, tx, database.Disk{Member: s.Name(), Path: data.Path, OSD: int(nr)})
		if err != nil {
			return fmt.Errorf("Failed to record disk: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	logger.Debugf("Created disk record for osd.%d", nr)

	dataPath := filepath.Join(os.Getenv("SNAP_COMMON"), "data")
	osdDataPath := filepath.Join(dataPath, "osd", fmt.Sprintf("ceph-%d", nr))

	// Keep the old path in case it changes after encrypting.
	oldPath := data.Path

	// Create directory.
	err = os.MkdirAll(osdDataPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap monitor: %w", err)
	}

	// Wipe and/or encrypt the disk if needed.
	err = prepareDisk(&data, "", osdDataPath, nr)

	// if we fail later, make sure we free up the record
	revert.Add(func() {
		os.RemoveAll(osdDataPath)
		s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
			database.DeleteDisk(ctx, tx, s.Name(), oldPath)
			return nil
		})
	})

	// Generate keyring.
	err = genAuth(filepath.Join(osdDataPath, "keyring"), fmt.Sprintf("osd.%d", nr), []string{"mgr", "allow profile osd"}, []string{"mon", "allow profile osd"}, []string{"osd", "allow *"})
	if err != nil {
		return fmt.Errorf("Failed to generate OSD keyring: %w", err)
	}

	// Generate OSD uuid.
	fsid := uuid.NewRandom().String()

	// Write fsid file.
	err = os.WriteFile(filepath.Join(osdDataPath, "fsid"), []byte(fsid), 0600)
	if err != nil {
		return fmt.Errorf("Failed to write fsid: %w", err)
	}

	// Bootstrap OSD.
	args := []string{"--mkfs", "--no-mon-config", "-i", fmt.Sprintf("%d", nr)}
	if wal != nil {
		err = prepareDisk(wal, ".wal", osdDataPath, nr)
		if err != nil {
			return fmt.Errorf("Failed to set up WAL device: %w", err)
		}
		args = append(args, []string{"--bluestore-block-wal-path", wal.Path}...)
	}
	if db != nil {
		err = prepareDisk(db, ".db", osdDataPath, nr)
		if err != nil {
			return fmt.Errorf("Failed to set up DB device: %w", err)
		}
		args = append(args, []string{"--bluestore-block-db-path", db.Path}...)
	}

	_, err = processExec.RunCommand("ceph-osd", args...)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap OSD: %w", err)
	}

	// Write the stamp file.
	err = os.WriteFile(filepath.Join(osdDataPath, "ready"), []byte(""), 0600)
	if err != nil {
		return fmt.Errorf("Failed to write stamp file: %w", err)
	}

	// Spawn the OSD.
	logger.Debugf("Spawning OSD %d", nr)
	err = snapRestart("osd", true)
	if err != nil {
		return fmt.Errorf("Failed to start osd.%d: %w", nr, err)
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
func RemoveOSD(s common.StateInterface, osd int64, bypassSafety bool, timeout int64) error {
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
func sanityCheck(s common.StateInterface, osd int64) error {
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
		return fmt.Errorf("ods.%d not found", osd)
	}
	return nil
}

// IsDowngradeNeeded checks if we need to downgrade the failure domain from 'host' to 'osd' level
// if we remove the given OSD
func IsDowngradeNeeded(s common.StateInterface, osd int64) (bool, error) {
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
func scaleDownFailureDomain(s common.StateInterface, osd int64) error {
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
		return fmt.Errorf("Failed to switch failure domain: %w", err)
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
		return fmt.Errorf("Failed to purge osd.%d: %w", osd, err)
	}
	logger.Infof("osd.%d purged", osd)
	return nil
}

func wipeDevice(s common.StateInterface, osd int64) {
	var err error
	// get the device path
	path, _ := database.OSDQuery.Path(s.ClusterState(), osd)
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

func doRemoveOSD(ctx context.Context, s common.StateInterface, osd int64, bypassSafety bool) error {
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
		return fmt.Errorf("Failed to check if osd.%d is present in Ceph: %w", osd, err)
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
	// stop the OSD service
	if isPresent {
		err = killOSD(osd)
	}
	if err != nil {
		return err
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
	// Wipe the underlying blocking device
	wipeDevice(s, osd)
	// Remove osd config
	err = removeOSDConfig(osd)
	if err != nil {
		return err
	}
	// Remove db entry
	err = database.OSDQuery.Delete(s.ClusterState(), osd)
	if err != nil {
		logger.Errorf("Failed to remove osd.%d from database: %v", osd, err)
		return fmt.Errorf("Failed to remove osd.%d from database: %w", osd, err)
	}
	return nil
}

func checkMinOSDs(s common.StateInterface, osd int64) error {
	// check if we have at least 3 OSDs post-removal
	disks, err := database.OSDQuery.List(s.ClusterState())
	if err != nil {
		return err
	}
	if len(disks) <= 3 {
		return fmt.Errorf("Cannot remove osd.%d we need at least 3 OSDs, have %d", osd, len(disks))
	}
	return nil
}

func outDownOSD(osd int64) error {
	_, err := processExec.RunCommand("ceph", "osd", "out", fmt.Sprintf("osd.%d", osd))
	if err != nil {
		logger.Errorf("Failed to take osd.%d out: %v", osd, err)
		return fmt.Errorf("Failed to take osd.%d out: %w", osd, err)
	}
	_, err = processExec.RunCommand("ceph", "osd", "down", fmt.Sprintf("osd.%d", osd))
	if err != nil {
		logger.Errorf("Failed to take osd.%d down: %v", osd, err)
		return fmt.Errorf("Failed to take osd.%d down: %w", osd, err)
	}
	return nil
}

func safetyCheckStop(osd int64) error {
	var safeStop bool

	retries := 12
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

	retries := 12
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
		return fmt.Errorf("Failed to remove osd.%d config: %w", osd, err)
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
		return false, fmt.Errorf("Failed to get ceph osd tree: %w", err)
	}
	// parse the json output
	var tree JSONData
	err = json.Unmarshal([]byte(out), &tree)
	if err != nil {
		logger.Errorf("Failed to parse ceph osd tree: %v", err)
		return false, fmt.Errorf("Failed to parse ceph osd tree: %w", err)
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
	cmdline := fmt.Sprintf("ceph-osd .* --id %d", osd)
	_, err := processExec.RunCommand("pkill", "-f", cmdline)
	if err != nil {
		logger.Errorf("Failed to kill osd.%d: %v", osd, err)
		return fmt.Errorf("Failed to kill osd.%d: %w", osd, err)
	}
	return nil
}
