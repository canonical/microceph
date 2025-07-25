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

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/spf13/afero"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/microcluster/v2/state"
	"github.com/pborman/uuid"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
)

// PathValidator provides an interface for validating device paths - introduced for mocking in tests.
type PathValidator interface {
	IsBlockdevPath(path string) bool
}

// SharedPathValidator is the production implementation using shared.IsBlockdevPath.
type SharedPathValidator struct{}

// IsBlockdevPath validates if the given path is a block device path.
func (v SharedPathValidator) IsBlockdevPath(path string) bool {
	return shared.IsBlockdevPath(path)
}

// MountChecker provides an interface for checking if devices are mounted - introduced for mocking in tests.
type MountChecker interface {
	IsMounted(device string) (bool, error)
}

// SharedMountChecker is the production implementation using common.IsMounted.
type SharedMountChecker struct{}

// IsMounted checks if the given device is mounted.
func (m SharedMountChecker) IsMounted(device string) (bool, error) {
	return common.IsMounted(device)
}

// FileStater provides an interface for getting file statistics - introduced for mocking in tests.
type FileStater interface {
	GetFileStat(path string) (uid int, gid int, major uint32, minor uint32, inode uint64, nlink int, err error)
}

// SharedFileStater is the production implementation using shared.GetFileStat.
type SharedFileStater struct{}

// GetFileStat gets file statistics for the given path.
func (f SharedFileStater) GetFileStat(path string) (uid int, gid int, major uint32, minor uint32, inode uint64, nlink int, err error) {
	return shared.GetFileStat(path)
}

// PristineChecker provides an interface for checking if a block device is pristine - introduced for mocking in tests.
type PristineChecker interface {
	IsPristineDisk(devicePath string) (bool, error)
}

// SharedPristineChecker is the production implementation for checking pristine disks.
type SharedPristineChecker struct{}

// IsPristineDisk checks if a block device is pristine by delegating to the common storage module.
func (p SharedPristineChecker) IsPristineDisk(devicePath string) (bool, error) {
	return common.IsPristineDisk(devicePath)
}

// OSDManager handles OSD operations. It holds the state, a runner for executing commands and a filesystem interface.
type OSDManager struct {
	state           state.State
	runner          common.Runner
	fs              afero.Fs
	storage         interfaces.StorageInterface
	validator       PathValidator
	mountChecker    MountChecker
	fileStater      FileStater
	pristineChecker PristineChecker
}

// NewOSDManager returns a new OSD manager instance.
func NewOSDManager(s state.State) *OSDManager {
	return &OSDManager{
		state:           s,
		runner:          common.ProcessExec,
		fs:              afero.NewOsFs(),
		storage:         StorageImpl{},
		validator:       SharedPathValidator{},
		mountChecker:    SharedMountChecker{},
		fileStater:      SharedFileStater{},
		pristineChecker: SharedPristineChecker{},
	}
}

func (m *OSDManager) prepareDisk(disk *types.DiskParameter, suffix string, osdPath string, osdID int64) error {
	// Check if device is mounted (only for actual block devices, not loop files)
	if m.validator.IsBlockdevPath(disk.Path) {
		mounted, err := m.mountChecker.IsMounted(disk.Path)
		if err != nil {
			return fmt.Errorf("failed to check if device %s is mounted: %w", disk.Path, err)
		}
		if mounted {
			return fmt.Errorf("device %s is currently mounted and cannot be used - aborting", disk.Path)
		}
	}

	if disk.Wipe {
		err := m.timeoutWipe(disk.Path)
		if err != nil {
			return fmt.Errorf("failed to wipe device %s: %w", disk.Path, err)
		}
	}
	if disk.Encrypt {
		err := m.checkEncryptSupport()
		if err != nil {
			return fmt.Errorf("encryption unsupported on this machine: %w", err)
		}
		path, err := m.setupEncryptedOSD(disk.Path, osdPath, osdID, suffix)
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
	link := filepath.Join(osdPath, "block")
	lfs, ok := m.fs.(afero.Linker)
	if !ok {
		return fmt.Errorf("%T doesn't support symlinks", m.fs)
	}
	err := lfs.SymlinkIfPossible(disk.Path, link)
	if err != nil {
		logger.Errorf("failed to symlink %s: %v", disk.Path, err)
		return err
	}
	return nil
}

// setupEncryptedOSD sets up an encrypted OSD on the given disk.
//
// Takes a path to the disk device as well as the OSD data path, the OSD id and
// a suffix (to differentiate invocations between data, WAL and DB devices).
// Returns the path to the encrypted device and an error if any.
func (m *OSDManager) setupEncryptedOSD(devicePath string, osdDataPath string, osdID int64, suffix string) (string, error) {
	lfs, ok := m.fs.(afero.Linker)
	if !ok {
		return "", fmt.Errorf("symlinks not supported by this filesystem")
	}
	err := lfs.SymlinkIfPossible(devicePath, filepath.Join(osdDataPath, "unencrypted"+suffix))
	if err != nil {
		logger.Errorf("failed to symlink unencrypted block device %s: %v", devicePath, err)
		return "", fmt.Errorf("failed to add unencrypted block symlink: %w", err)
	}

	// Create a key for the encrypted device
	key, err := createKey()
	if err != nil {
		return "", fmt.Errorf("key creation error: %w", err)
	}

	// Store key in ceph key value store
	err = m.storeKey(key, osdID, suffix)
	if err != nil {
		return "", fmt.Errorf("key store error: %w", err)
	}

	// Encrypt the device
	encryptDevice(devicePath, key)
	if err != nil {
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
	_, err = stdin.Write(key)
	if err != nil {
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
func (m *OSDManager) storeKey(key []byte, osdID int64, suffix string) error {
	// Run the ceph config-key set command
	_, err := m.runner.RunCommand("ceph", "config-key", "set", fmt.Sprintf("microceph:osd%s.%d/key", suffix, osdID), string(key))
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
	_, err = stdin.Write(key)
	if err != nil {
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
func (m *OSDManager) checkEncryptSupport() error {
	// Check if we have a mapper
	_, err := m.fs.Stat("/dev/mapper/control")
	if err != nil {
		return fmt.Errorf("missing /dev/mapper/control: %w", err)
	}

	// Check if the dm-crypt interface is not connected.
	if !isIntfConnected("dm-crypt") {
		helper := "use \"sudo snap connect microceph:dm-crypt ; sudo snap restart microceph.daemon\" to enable encryption."
		return fmt.Errorf("dm-crypt interface connection missing: \n%s", helper)
	}

	// Check if we have the dm_crypt module
	inf, err := m.fs.Stat("/sys/module/dm_crypt")
	if err != nil || inf == nil || !inf.IsDir() {
		return fmt.Errorf("missing dm_crypt module: %w", err)
	}

	// Check if we can list the /run directory; older snapd had an issue with this, https://github.com/snapcore/snapd/pull/12445
	_, err = afero.ReadDir(m.fs, "/run")
	if err != nil {
		return fmt.Errorf("can't access /run, might need to update snapd to >=2.59.1: %w", err)
	}
	return nil
}

// switchFailureDomain switches the crush rules failure domain from old to new
func (m *OSDManager) switchFailureDomain(old string, new string) error {
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
func (m *OSDManager) updateFailureDomain(ctx context.Context, s state.State) error {
	logger.Infof("Checking if we need to update failure domain for OSDs")
	numNodes, err := database.MemberCounter.Count(ctx, s)
	if err != nil {
		return fmt.Errorf("failed to count members: %w", err)
	}

	if numNodes >= 3 {
		logger.Infof("We have %d nodes, switching failure domain to host", numNodes)
		err = m.switchFailureDomain("osd", "host")
		if err != nil {
			return fmt.Errorf("failed to set host failure domain: %w", err)
		}
		logger.Infof("Successfully switched failure domain to host")
	}
	return nil
}

func (m *OSDManager) setStablePath(storage *api.ResourcesStorage, param *types.DiskParameter) error {
	// Validate the path.
	if !m.validator.IsBlockdevPath(param.Path) {
		logger.Errorf("not a block device path: %s", param.Path)
		return fmt.Errorf("invalid disk path: %s", param.Path)
	}

	_, _, major, minor, _, _, err := m.fileStater.GetFileStat(param.Path)
	if err != nil {
		logger.Errorf("failed to get block device path %s: %v", param.Path, err)
		return fmt.Errorf("invalid disk path: %w", err)
	}

	dev := fmt.Sprintf("%d:%d", major, minor)

	for _, disk := range storage.Disks {
		// Check if full disk.
		if disk.Device == dev {
			candidate := fmt.Sprintf("/dev/disk/by-id/%s", disk.DeviceID)

			// check if candidate exists
			if exists, _ := afero.Exists(m.fs, candidate); exists {
				if isDir, _ := afero.IsDir(m.fs, candidate); !isDir {
					param.Path = candidate
				}
			} else {
				candidate = fmt.Sprintf("/dev/disk/by-path/%s", disk.DevicePath)
				if exists, _ := afero.Exists(m.fs, candidate); exists {
					if isDir, _ := afero.IsDir(m.fs, candidate); !isDir {
						param.Path = candidate
					}
				}
			}

			break
		}

		// Check if partition.
		for _, part := range disk.Partitions {
			if part.Device == dev {
				candidate := fmt.Sprintf("/dev/disk/by-id/%s-part%d", disk.DeviceID, part.Partition)
				if exists, _ := afero.Exists(m.fs, candidate); exists {
					param.Path = candidate
				} else {
					candidate = fmt.Sprintf("/dev/disk/by-path/%s-part%d", disk.DevicePath, part.Partition)
					if exists, _ := afero.Exists(m.fs, candidate); exists {
						param.Path = candidate
					}
				}

				break
			}
		}
	}
	logger.Infof("Set stable path for to %s", param.Path)
	return nil
}

// checkDeviceHasPartitions checks if a block device has partitions using the LXD resource API.
// Returns true if the device has partitions, false otherwise.
func (m *OSDManager) checkDeviceHasPartitions(storage *api.ResourcesStorage, devicePath string) (bool, error) {
	// Only check block devices
	if !m.validator.IsBlockdevPath(devicePath) {
		return false, nil
	}

	_, _, major, minor, _, _, err := m.fileStater.GetFileStat(devicePath)
	if err != nil {
		return false, fmt.Errorf("failed to get device info for %s: %w", devicePath, err)
	}

	dev := fmt.Sprintf("%d:%d", major, minor)

	// Find the disk in storage info
	for _, disk := range storage.Disks {
		if disk.Device == dev {
			// Check if this disk has any partitions
			if len(disk.Partitions) > 0 {
				logger.Debugf("Device %s has %d partitions", devicePath, len(disk.Partitions))
				return true, nil
			}
			break
		}
	}

	return false, nil
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
func (m *OSDManager) createBackingFile(dir string, size uint64) (string, error) {
	backing := filepath.Join(dir, "osd-backing.img")
	_, err := m.runner.RunCommand("truncate", "-s", fmt.Sprintf("%dM", size), backing)
	if err != nil {
		return "", fmt.Errorf("failed to create backing file %s: %w", backing, err)
	}
	return backing, nil
}

// addLoopBackOSDs adds OSDs to the cluster backed by loopback files
func (m *OSDManager) addLoopBackOSDs(ctx context.Context, spec string) error {
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
		err = m.doAddOSD(ctx, types.DiskParameter{LoopSize: size}, nil, nil)
		if err != nil {
			return fmt.Errorf("failed to add loop OSD: %w", err)
		}
	}
	return nil
}

// checkPartitionsOnDevice checks if a device has partitions and returns an error if it does (unless wipe is enabled)
func (m *OSDManager) checkPartitionsOnDevice(disk *types.DiskParameter, storage *api.ResourcesStorage, deviceType string) error {
	if !disk.Wipe {
		hasPartitions, err := m.checkDeviceHasPartitions(storage, disk.Path)
		if err != nil {
			return fmt.Errorf("failed to check partitions on %s device %s: %w", deviceType, disk.Path, err)
		}
		if hasPartitions {
			return fmt.Errorf("%s device %s has partitions - use --wipe to override", deviceType, disk.Path)
		}
	}
	return nil
}

// checkPristineDevice checks if a device is pristine and returns an error if it's not (unless wipe is enabled)
func (m *OSDManager) checkPristineDevice(disk *types.DiskParameter, deviceType string) error {
	if !disk.Wipe {
		isPristine, err := m.pristineChecker.IsPristineDisk(disk.Path)
		if err != nil {
			return fmt.Errorf("failed to check if %s device %s is pristine: %w", deviceType, disk.Path, err)
		}
		if !isPristine {
			return fmt.Errorf("%s device %s is not pristine (contains data) - use --wipe to override", deviceType, disk.Path)
		}
	}
	return nil
}

// bootstrapOSD bootstraps an OSD.
func (m *OSDManager) bootstrapOSD(osdDataPath string, nr int64, wal, db *types.DiskParameter, storage *api.ResourcesStorage) error {
	logger.Infof("Bootstrapping OSD %s to %d", osdDataPath, nr)
	var err error

	args := []string{"--mkfs", "--no-mon-config", "-i", fmt.Sprintf("%d", nr)}
	if wal != nil {
		err = m.setStablePath(storage, wal)
		if err != nil {
			return fmt.Errorf("failed to set stable path for WAL: %w", err)
		}

		// Check for partitions on WAL device unless wipe is enabled
		err = m.checkPartitionsOnDevice(wal, storage, "WAL")
		if err != nil {
			return err
		}

		// Check if WAL device is pristine unless wipe is enabled
		err = m.checkPristineDevice(wal, "WAL")
		if err != nil {
			return err
		}

		err = m.prepareDisk(wal, ".wal", osdDataPath, nr)
		if err != nil {
			return fmt.Errorf("failed to set up WAL device: %w", err)
		}
		args = append(args, []string{"--bluestore-block-wal-path", wal.Path}...)
	}
	if db != nil {
		err = m.setStablePath(storage, db)
		if err != nil {
			return fmt.Errorf("failed to set stable path for DB: %w", err)
		}

		// Check for partitions on DB device unless wipe is enabled
		err = m.checkPartitionsOnDevice(db, storage, "DB")
		if err != nil {
			return err
		}

		// Check if DB device is pristine unless wipe is enabled
		err = m.checkPristineDevice(db, "DB")
		if err != nil {
			return err
		}

		err = m.prepareDisk(db, ".db", osdDataPath, nr)
		if err != nil {
			return fmt.Errorf("failed to set up DB device: %w", err)
		}
		args = append(args, []string{"--bluestore-block-db-path", db.Path}...)
	}

	_, err = m.runner.RunCommand("ceph-osd", args...)
	if err != nil {
		return fmt.Errorf("failed to bootstrap OSD: %w", err)
	}

	// Write the stamp file.
	err = afero.WriteFile(m.fs, filepath.Join(osdDataPath, "ready"), []byte(""), 0600)
	if err != nil {
		return fmt.Errorf("failed to write stamp file: %w", err)
	}
	logger.Infof("OSD %s bootstrapped successfully", osdDataPath)
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

// addBulkDisks adds multiple disks as OSDs and generates the API response for request.
func (m *OSDManager) addBulkDisks(ctx context.Context, disks []types.DiskParameter, wal *types.DiskParameter, db *types.DiskParameter) types.DiskAddResponse {
	ret := types.DiskAddResponse{}

	if len(disks) == 1 {
		// Add single disk with requested WAL/DB devices.
		resp := m.addSingleDisk(ctx, disks[0], wal, db)
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
		resp := m.addSingleDisk(ctx, disk, nil, nil)
		ret.Reports = append(ret.Reports, resp)
	}

	return ret
}

// addSingleDisk is a wrapper around AddOSD which logs disk addition failures and returns a formatted response.
func (m *OSDManager) addSingleDisk(ctx context.Context, disk types.DiskParameter, wal *types.DiskParameter, db *types.DiskParameter) types.DiskAddReport {
	if strings.Contains(disk.Path, constants.LoopSpecId) {
		// Add file based OSDs.
		err := m.addLoopBackOSDs(ctx, disk.Path)
		if err != nil {
			logger.Errorf("failed to add disk: spec %s, err %v", disk.Path, err)
			return types.DiskAddReport{Path: disk.Path, Report: "Failure", Error: err.Error()}
		}
	} else {
		// Add physical disk based OSD.
		err := m.doAddOSD(ctx, disk, wal, db)
		if err != nil {
			logger.Errorf("failed to add disk: path %s, err %v", disk.Path, err)
			// return failure as response.
			return types.DiskAddReport{Path: disk.Path, Report: "Failure", Error: err.Error()}
		}
	}

	// return success as response.
	return types.DiskAddReport{Path: disk.Path, Report: "Success", Error: ""}
}

// addOSD adds an OSD to the cluster, given the data, WAL and DB devices and their respective
// flags for wiping and encrypting.
func (m *OSDManager) addOSD(ctx context.Context, data types.DiskParameter, wal *types.DiskParameter, db *types.DiskParameter) error {
	logger.Infof("addOSD, params: %s, WAL: %v, DB: %v", data.Path, wal, db)

	err := m.validateAddOSDArgs(data, wal, db)
	if err != nil {
		return err
	}

	return m.doAddOSD(ctx, data, wal, db)
}

func (m *OSDManager) validateAddOSDArgs(data types.DiskParameter, wal *types.DiskParameter, db *types.DiskParameter) error {
	if data.LoopSize != 0 && (wal != nil || db != nil) {
		return fmt.Errorf("loopback and WAL/DB are mutually exclusive")
	}
	return nil
}

func (m *OSDManager) stabilizeDevicePath(data *types.DiskParameter) (*api.ResourcesStorage, error) {
	logger.Infof("Stabilizing device path for %s", data.Path)
	if data.LoopSize != 0 {
		return nil, nil
	}

	storage, err := m.storage.GetStorage()
	if err != nil {
		logger.Errorf("failed to stabilize device path for %s, err %v", data.Path, err)
		return nil, fmt.Errorf("unable to list system disks: %w", err)
	}
	err = m.setStablePath(storage, data)
	if err != nil {
		logger.Errorf("failed to set stable path for %s, err %v", data.Path, err)
		return nil, fmt.Errorf("failed to set stable disk path: %w", err)
	}
	logger.Infof("Stabilized device path for %s", data.Path)
	return storage, nil
}

func (m *OSDManager) createDiskRecord(ctx context.Context, data *types.DiskParameter) (int64, error) {
	var nr int64
	err := m.state.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		nr, err = database.CreateDisk(ctx, tx, database.Disk{Member: m.state.Name(), Path: data.Path})
		if err != nil {
			return fmt.Errorf("failed to record disk: %w", err)
		}
		return nil
	})
	if err != nil {
		return -1, err
	}

	logger.Infof("Created disk record for osd.%d", nr)
	return nr, nil
}

func getOSDDataPath(nr int64) string {
	return filepath.Join(constants.GetPathConst().DataPath, "osd", fmt.Sprintf("ceph-%d", nr))
}

func (m *OSDManager) setupRevert(ctx context.Context, data *types.DiskParameter, osdDataPath string) *revert.Reverter {
	revt := revert.New()
	revt.Add(func() {
		// try to cleanup, but don't fail
		_ = m.fs.RemoveAll(osdDataPath)
		_ = m.state.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			_ = database.DeleteDisk(ctx, tx, m.state.Name(), data.Path)
			return nil
		})
	})
	return revt
}

func (m *OSDManager) prepareOSDData(ctx context.Context, data *types.DiskParameter, osdDataPath string, nr int64) error {
	err := m.fs.MkdirAll(osdDataPath, 0700)
	if err != nil {
		logger.Errorf("failed to create dir %s, err %v", osdDataPath, err)
		return fmt.Errorf("failed to create OSD directory: %w", err)
	}

	if data.LoopSize != 0 {
		backing, err := m.createBackingFile(osdDataPath, data.LoopSize)
		if err != nil {
			return err
		}
		data.Path = backing
		// update db, it didn't have a path before
		err = m.state.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			err = database.OSDQuery.UpdatePath(ctx, m.state, nr, backing)
			if err != nil {
				return fmt.Errorf("failed to update disk record: %w", err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *OSDManager) generateOSDFiles(osdDataPath string, nr int64) error {
	err := genAuth(filepath.Join(osdDataPath, "keyring"), fmt.Sprintf("osd.%d", nr), []string{"mgr", "allow profile osd"}, []string{"mon", "allow profile osd"}, []string{"osd", "allow *"})
	if err != nil {
		logger.Errorf("failed to generate OSD files, osd path %s, err %v", osdDataPath, err)
		return fmt.Errorf("failed to generate OSD keyring: %w", err)
	}

	fsid := uuid.NewRandom().String()
	err = afero.WriteFile(m.fs, filepath.Join(osdDataPath, "fsid"), []byte(fsid), 0600)
	if err != nil {
		logger.Errorf("failed to write fsid, osd path %s, err %v", osdDataPath, err)
		return fmt.Errorf("failed to write fsid: %w", err)
	}
	logger.Infof("Generated OSD files for osd.%d, fsid %s", nr, fsid)
	return nil
}

func (m *OSDManager) spawnOSD(nr int64) error {
	logger.Infof("Spawning OSD %d", nr)
	err := snapRestart("osd", true)
	if err != nil {
		return fmt.Errorf("failed to start osd.%d: %w", nr, err)
	}
	return nil
}

// doAddOSD is the internal implementation for adding an OSD to the cluster.
func (m *OSDManager) doAddOSD(ctx context.Context, data types.DiskParameter, wal *types.DiskParameter, db *types.DiskParameter) error {
	storage, err := m.stabilizeDevicePath(&data)
	if err != nil {
		logger.Errorf("failed to stabilize device path for %s: %v", data.Path, err)
		return err
	}

	nr, err := m.createDiskRecord(ctx, &data)
	if err != nil {
		logger.Errorf("failed to create disk record for %s: %v", data.Path, err)
		return err
	}

	osdDataPath := getOSDDataPath(nr)
	logger.Infof("osd data path: %s", osdDataPath)
	revert := m.setupRevert(ctx, &data, osdDataPath)
	defer revert.Fail()

	err = m.prepareOSDData(ctx, &data, osdDataPath, nr)
	if err != nil {
		logger.Errorf("failed to prepare OSD data for %s: %v", data.Path, err)
		return err
	}

	// Check for partitions on data device unless wipe is enabled
	if storage != nil {
		err = m.checkPartitionsOnDevice(&data, storage, "data")
		if err != nil {
			return err
		}
	}

	// Check if data device is pristine unless wipe is enabled
	if storage != nil {
		err = m.checkPristineDevice(&data, "data")
		if err != nil {
			return err
		}
	}

	err = m.prepareDisk(&data, "", osdDataPath, nr)
	if err != nil {
		logger.Errorf("failed to prepare disk for %s: %v", data.Path, err)
		return fmt.Errorf("failed to prepare data device: %w", err)
	}

	err = m.generateOSDFiles(osdDataPath, nr)
	if err != nil {
		logger.Errorf("failed to generate OSD files for %s: %v", data.Path, err)
		return err
	}

	err = m.bootstrapOSD(osdDataPath, nr, wal, db, storage)
	if err != nil {
		logger.Errorf("failed to bootstrap OSD %d: %v", nr, err)
		return err
	}

	err = m.spawnOSD(nr)
	if err != nil {
		logger.Errorf("failed to spawn OSD %d: %v", nr, err)
		return err
	}

	err = m.updateFailureDomain(ctx, m.state)
	if err != nil {
		logger.Errorf("failed to update failure domain after adding OSD %d: %v", nr, err)
		return err
	}

	revert.Success()
	logger.Infof("Added osd.%d", nr)
	return nil
}

// AddLoopBackOSDs adds OSDs backed by loopback files using a one-off manager.
func AddLoopBackOSDs(ctx context.Context, s state.State, spec string) error {
	return NewOSDManager(s).addLoopBackOSDs(ctx, spec)
}

// AddBulkDisks adds multiple disks using a one-off manager.
func AddBulkDisks(ctx context.Context, s state.State, disks []types.DiskParameter, wal *types.DiskParameter, db *types.DiskParameter) types.DiskAddResponse {
	return NewOSDManager(s).addBulkDisks(ctx, disks, wal, db)
}

// AddSingleDisk adds a single disk using a one-off manager.
func AddSingleDisk(ctx context.Context, s state.State, disk types.DiskParameter, wal *types.DiskParameter, db *types.DiskParameter) types.DiskAddReport {
	return NewOSDManager(s).addSingleDisk(ctx, disk, wal, db)
}

// AddOSD adds an OSD using a one-off manager.
func AddOSD(ctx context.Context, s state.State, data types.DiskParameter, wal *types.DiskParameter, db *types.DiskParameter) error {
	return NewOSDManager(s).addOSD(ctx, data, wal, db)
}

// ListOSD lists current OSD disks
func ListOSD(ctx context.Context, s state.State) (types.Disks, error) {
	return database.OSDQuery.List(ctx, s)
}

// RemoveOSD removes an OSD disk
func RemoveOSD(ctx context.Context, s interfaces.StateInterface, osd int64, bypassSafety bool, timeout int64) error {
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
func sanityCheck(ctx context.Context, s interfaces.StateInterface, osd int64) error {
	// check osd is positive
	if osd < 0 {
		return fmt.Errorf("OSD must be a positive integer")
	}

	// check if the OSD exists in the database
	exists, err := database.OSDQuery.HaveOSD(ctx, s.ClusterState(), osd)
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
func IsDowngradeNeeded(ctx context.Context, s interfaces.StateInterface, osd int64) (bool, error) {
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
	numNodes, err := database.MemberCounter.CountExclude(ctx, s.ClusterState(), osd)
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
func scaleDownFailureDomain(ctx context.Context, s interfaces.StateInterface, osd int64) error {
	m := NewOSDManager(s.ClusterState())
	needDowngrade, err := IsDowngradeNeeded(ctx, s, osd)
	logger.Debugf("Downgrade needed: %v", needDowngrade)
	if err != nil {
		return err
	}
	if !needDowngrade {
		return nil
	}
	err = m.switchFailureDomain("host", "osd")
	if err != nil {
		return fmt.Errorf("failed to switch failure domain: %w", err)
	}
	return nil
}

// reweightOSD reweights the given OSD to the given weight
func (m *OSDManager) reweightOSD(ctx context.Context, osd int64, weight float64) {
	logger.Debugf("Reweighting osd.%d to %f", osd, weight)
	_, err := m.runner.RunCommand(
		"ceph", "osd", "crush", "reweight",
		fmt.Sprintf("osd.%d", osd),
		fmt.Sprintf("%f", weight),
	)
	if err != nil {
		// only log a warn, don't treat fail to reweight as a fatal error
		logger.Warnf("Failed to reweight osd.%d: %v", osd, err)
	}
}

func (m *OSDManager) doPurge(osd int64) error {
	// run ceph osd purge command
	_, err := m.runner.RunCommand(
		"ceph", "osd", "purge", fmt.Sprintf("osd.%d", osd),
		"--yes-i-really-mean-it",
	)
	return err
}

func (m *OSDManager) purgeOSD(osd int64) error {
	logger.Infof("Purging osd.%d", osd)
	var err error
	retries := 10
	var backoff time.Duration

	for i := 0; i < retries; i++ {
		err = m.doPurge(osd)
		if err == nil {
			// Success: break the retry loop
			break
		}
		// we're getting a RunError from common.ProcessExec.RunCommand, and it
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

func (m *OSDManager) wipeDevice(ctx context.Context, path string) {
	var err error
	logger.Infof("wipeDevice %s", path)
	// wipe the device, retry with exponential backoff
	retries := 8
	var backoff time.Duration
	for i := 0; i < retries; i++ {
		err = m.timeoutWipe(path)
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
func (m *OSDManager) timeoutWipe(path string) error {
	logger.Infof("timeoutWipe device %s", path)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := m.runner.RunCommandContext(
		ctx,
		"dd", "if=/dev/zero",
		fmt.Sprintf("of=%s", path),
		"bs=4M", "count=10", "status=none",
	)
	logger.Infof("Wipe command finished, err: %v", err)
	return err
}

func doRemoveOSD(ctx context.Context, s interfaces.StateInterface, osd int64, bypassSafety bool) error {
	var err error
	m := NewOSDManager(s.ClusterState())

	// general sanity
	err = sanityCheck(ctx, s, osd)
	if err != nil {
		return err
	}

	if !bypassSafety {
		// check: at least 3 OSDs
		err = checkMinOSDs(ctx, s, osd)
		if err != nil {
			return err
		}
	}

	err = scaleDownFailureDomain(ctx, s, osd)
	if err != nil {
		return err
	}

	// check if the osd is still in the cluster -- if we're being re-run, it might not be
	isPresent, err := m.haveOSDInCeph(osd)
	if err != nil {
		return fmt.Errorf("failed to check if osd.%d is present in Ceph: %w", osd, err)
	}
	// reweight/drain data
	if isPresent {
		m.reweightOSD(ctx, osd, 0)
	}
	// perform safety check for stopping
	if isPresent && !bypassSafety {
		err = m.safetyCheckStop([]int64{osd})
		if err != nil {
			return err
		}
	}
	// take the OSD out and down
	if isPresent {
		err = m.outDownOSD(osd)
		if err != nil {
			return err
		}
	}
	// stop the OSD service, but don't fail if it's not running
	if isPresent {
		_ = m.killOSD(osd)
	}
	// perform safety check for destroying
	if isPresent && !bypassSafety {
		err = m.safetyCheckDestroy(osd)
		if err != nil {
			return err
		}
	}
	// purge the OSD
	if isPresent {
		err = m.purgeOSD(osd)
		if err != nil {
			return err
		}
	}

	err = m.clearStorage(ctx, s, osd)
	if err != nil {
		// log error but don't fail, we still want to remove the OSD from the cluster
		logger.Errorf("Failed to clear storage for osd.%d: %v", osd, err)
	}

	// Remove osd config
	err = m.removeOSDConfig(osd)
	if err != nil {
		return err
	}
	// Remove db entry
	err = database.OSDQuery.Delete(ctx, s.ClusterState(), osd)
	if err != nil {
		logger.Errorf("Failed to remove osd.%d from database: %v", osd, err)
		return fmt.Errorf("failed to remove osd.%d from database: %w", osd, err)
	}
	return nil
}

func (m *OSDManager) clearStorage(ctx context.Context, s interfaces.StateInterface, osd int64) error {
	path, err := database.OSDQuery.Path(ctx, s.ClusterState(), osd)
	if err != nil {
		return err
	}

	var fileInfo os.FileInfo
	lfs, ok := m.fs.(afero.Lstater)
	if !ok {
		logger.Errorf("%T does not implement afero.Lstater", m.fs)
		return fmt.Errorf("filesystem does not support lstat: %T", m.fs)
	}
	fileInfo, _, err = lfs.LstatIfPossible(path) // ignore "was lstat used" flag
	if err != nil {
		return err
	}

	// Typically we'll be dealing with a symlink, but lets check for safety
	if fileInfo.Mode()&os.ModeSymlink != 0 {
		fileInfo, err = m.fs.Stat(path) // Follow the symlink
		if err != nil {
			return err
		}
	}
	if fileInfo.Mode()&os.ModeDevice != 0 {
		// wipe the device
		m.wipeDevice(ctx, path)
	}
	// backing files etc. are being removed later along with config
	return nil
}

func checkMinOSDs(ctx context.Context, s interfaces.StateInterface, osd int64) error {
	// check if we have at least 3 OSDs post-removal
	disks, err := database.OSDQuery.List(ctx, s.ClusterState())
	if err != nil {
		return err
	}
	if len(disks) <= 3 {
		return fmt.Errorf("cannot remove osd.%d we need at least 3 OSDs, have %d", osd, len(disks))
	}
	return nil
}

func (m *OSDManager) outDownOSD(osd int64) error {
	_, err := m.runner.RunCommand("ceph", "osd", "out", fmt.Sprintf("osd.%d", osd))
	if err != nil {
		logger.Errorf("Failed to take osd.%d out: %v", osd, err)
		return fmt.Errorf("failed to take osd.%d out: %w", osd, err)
	}
	_, err = m.runner.RunCommand("ceph", "osd", "down", fmt.Sprintf("osd.%d", osd))
	if err != nil {
		logger.Errorf("Failed to take osd.%d down: %v", osd, err)
		return fmt.Errorf("failed to take osd.%d down: %w", osd, err)
	}
	return nil
}

func setOsdNooutFlag(set bool) error {
	var command string

	switch set {
	case true:
		command = "set"
	case false:
		command = "unset"
	}

	_, err := common.ProcessExec.RunCommand("ceph", "osd", command, "noout")
	if err != nil {
		logger.Errorf("failed to %s noout flag: %v", command, err)
		return fmt.Errorf("failed to %s noout flag: %w", command, err)
	}
	return nil
}

func isOsdNooutSet() (bool, error) {
	output, err := common.ProcessExec.RunCommand("ceph", "osd", "dump")
	if err != nil {
		logger.Errorf("failed to dump osd info: %v", err)
		return false, fmt.Errorf("failed to dump osd info: %w", err)
	}
	logger.Infof("osd dump: %s", output)
	return strings.Contains(output, "noout"), nil
}

func (m *OSDManager) safetyCheckStop(osds []int64) error {
	var safeStop bool

	retries := 16
	var backoff time.Duration

	for i := 0; i < retries; i++ {
		safeStop = m.testSafeStop(osds)
		if safeStop {
			// Success: break the retry loop
			break
		}
		backoff = time.Duration(math.Pow(2, float64(i))) * time.Millisecond * 100
		logger.Infof("osd.%v not ok to stop, retrying in %v", osds, backoff)
		time.Sleep(backoff)
	}
	if !safeStop {
		logger.Errorf("osd.%v failed to reach ok-to-stop", osds)
		return fmt.Errorf("osd.%d failed to reach ok-to-stop", osds)
	}
	logger.Infof("osd.%d ok to stop", osds)
	return nil
}

func (m *OSDManager) safetyCheckDestroy(osd int64) error {
	var safeDestroy bool

	retries := 16
	var backoff time.Duration

	for i := 0; i < retries; i++ {
		safeDestroy = m.testSafeDestroy(osd)
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

func (m *OSDManager) testSafeDestroy(osd int64) bool {
	// run ceph osd safe-to-destroy
	_, err := m.runner.RunCommand("ceph", "osd", "safe-to-destroy", fmt.Sprintf("osd.%d", osd))
	return err == nil
}

func (m *OSDManager) testSafeStop(osds []int64) bool {
	// run ceph osd ok-to-stop
	args := []string{"osd", "ok-to-stop"}
	for _, osd := range osds {
		args = append(args, fmt.Sprintf("osd.%d", osd))
	}
	_, err := m.runner.RunCommand("ceph", args...)
	return err == nil
}

func (m *OSDManager) removeOSDConfig(osd int64) error {
	dataPath := filepath.Join(os.Getenv("SNAP_COMMON"), "data")
	osdDataPath := filepath.Join(dataPath, "osd", fmt.Sprintf("ceph-%d", osd))
	err := m.fs.RemoveAll(osdDataPath)
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
func (m *OSDManager) haveOSDInCeph(osd int64) (bool, error) {
	// run ceph osd tree
	out, err := m.runner.RunCommand("ceph", "osd", "tree", "-f", "json")
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
func (m *OSDManager) killOSD(osd int64) error {
	cmdline := fmt.Sprintf("ceph-osd .* --id %d$", osd)
	_, err := m.runner.RunCommand("pkill", "-f", cmdline)
	if err != nil {
		logger.Errorf("Failed to kill osd.%d: %v", osd, err)
		return fmt.Errorf("failed to kill osd.%d: %w", osd, err)
	}
	return nil
}

func SetReplicationFactor(pools []string, size int64) error {
	ssize := fmt.Sprintf("%d", size)
	_, err := common.ProcessExec.RunCommand("ceph", "config", "set", "global",
		"osd_pool_default_size", ssize)
	if err != nil {
		return fmt.Errorf("failed to set pool size default: %w", err)
	}

	allowSizeOne := "true"
	if size != 1 {
		allowSizeOne = "false"
	}

	_, err = common.ProcessExec.RunCommand("ceph", "config", "set", "global",
		"mon_allow_pool_size_one", allowSizeOne)
	if err != nil {
		return fmt.Errorf("failed to set size one pool config option: %w", err)
	}

	if len(pools) == 1 && pools[0] == "*" {
		// Apply setting to all existing pools.
		out, err := common.ProcessExec.RunCommand("ceph", "osd", "pool", "ls", "--format", "json")
		if err != nil {
			return fmt.Errorf("failed to list pools: %w", err)
		}

		err = json.Unmarshal([]byte(out), &pools)
		if err != nil {
			return fmt.Errorf("Failed to parse OSD pool names: %w", err)
		}
	}

	for _, pool := range pools {
		pool = strings.TrimSpace(pool)
		if pool == "" {
			continue
		}

		_, err := common.ProcessExec.RunCommand("ceph", "osd", "pool", "set", pool, "size", ssize, "--yes-i-really-mean-it")
		if err != nil {
			return fmt.Errorf("failed to set pool size for %s: %w", pool, err)
		}
	}

	return nil
}

// GetOSDPools returns a list of OSD Pools and their configurations.
func GetOSDPools() ([]types.Pool, error) {
	out, err := common.ProcessExec.RunCommand("ceph", "osd", "pool", "ls", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("failed to list pools: %w", err)
	}

	var poolNames []string
	err = json.Unmarshal([]byte(out), &poolNames)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse OSD pool names: %w", err)
	}

	pools := make([]types.Pool, 0, len(poolNames))
	for _, name := range poolNames {
		out, err := common.ProcessExec.RunCommand("ceph", "osd", "pool", "get", name, "all", "--format", "json")
		if err != nil {
			return nil, fmt.Errorf("Failed to fetch configuration for OSD pool %q: %w", name, err)
		}

		var pool types.Pool
		err = json.Unmarshal([]byte(out), &pool)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q OSD pool configuration: %w", name, err)
		}

		pools = append(pools, pool)
	}

	return pools, nil
}

// CephPool abstracts the paramters of a ceph pool as provided by `osd pool ls detail`
type CephPool struct {
	Id          int                    `json:"pool_id" yaml:"pool_id"`
	Name        string                 `json:"pool_name" yaml:"pool_name"`
	Application map[string]interface{} `json:"application_metadata" yaml:"application_metadata"`
}

// ListPools lists the current pools on the ceph cluster,
// Additionally filtered for requested application name.
func ListPools(application string) []CephPool {
	args := []string{"osd", "pool", "ls", "detail", "--format", "json"}

	output, err := common.ProcessExec.RunCommand("ceph", args...)
	if err != nil {
		return []CephPool{}
	}

	logger.Infof("OSD: Pool list %s", output)

	ret := []CephPool{}
	err = json.Unmarshal([]byte(output), &ret)
	if err != nil {
		logger.Warnf("Failed to Unmarshal pool details: %v", err)
		return []CephPool{}
	}

	// if no application filter provided.
	if len(application) == 0 {
		return ret
	}

	// filtered return slice of maximum needed size.
	filterdRet := make([]CephPool, len(ret))
	counter := 0
	for _, cephPool := range ret {
		_, ok := cephPool.Application[application]
		if ok {
			// append to the filter slice.
			logger.Infof("OSD: Found match(%s) for application(%s)", cephPool.Name, application)
			filterdRet[counter] = cephPool
			counter++
		}
	}

	logger.Infof("OSD: Filtered Pool list %v", filterdRet)
	return filterdRet
}

// SetOsdState start or stop OSD service
func SetOsdState(up bool) error {
	var err error

	switch up {
	case true:
		err = snapStart("osd", true)
	case false:
		err = snapStop("osd", true)
	}

	if err != nil {
		return fmt.Errorf("failed to change the state of osd service: %w", err)
	}
	return nil
}
