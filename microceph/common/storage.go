package common

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/logger"
	"github.com/spf13/afero"
	"golang.org/x/sys/unix"
)

// IsMounted checks if a device is mounted.
func IsMounted(device string) (bool, error) {
	return IsMountedWithFs(device, afero.NewOsFs())
}

// IsMountedWithFs checks if a device is mounted using the provided filesystem.
func IsMountedWithFs(device string, fs afero.Fs) (bool, error) {
	// Resolve any symlink and get the absolute path of the device.
	resolvedPath, err := filepath.EvalSymlinks(filepath.Join(constants.GetPathConst().RootFs, device))
	if err != nil {
		// Handle errors other than not existing differently as EvalSymlinks takes care of symlink resolution
		return false, err
	}

	// Use findmnt to check if the device is mounted, more reliable than checking /proc/mounts directly.
	// findmnt --source returns 0 if the device is mounted, 1 if not
	_, err = ProcessExec.RunCommand("findmnt", "--source", resolvedPath)
	if err != nil {
		// Try to unwrap and check the original error
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			if exitError.ExitCode() == 1 {
				// Exit code 1 means device not found/not mounted
				return false, nil
			}
		}
		// Other errors (command not found, permission issues, etc.)
		return false, err
	}

	// Exit code 0 means device is mounted
	return true, nil
}

// IsCephDevice checks if a given device is used as either a WAL or DB block device in any Ceph OSD.
func IsCephDevice(device string) (bool, error) {
	return IsCephDeviceWithFs(device, afero.NewOsFs())
}

// IsCephDeviceWithFs checks if a given device is used as either a WAL or DB block device in any Ceph OSD using the provided filesystem.
func IsCephDeviceWithFs(device string, fs afero.Fs) (bool, error) {
	// Resolve the given device path first to handle any symlinks
	resolved, err := filepath.EvalSymlinks(device)
	if err != nil {
		logger.Errorf("failed to resolve device path: %v", err)
		return false, err
	}
	// Check all ceph data dirs
	baseDir := filepath.Join(constants.GetPathConst().DataPath, "osd")
	osdDirs, err := afero.ReadDir(fs, baseDir)
	if err != nil {
		// Likely no OSDs exist yet
		logger.Debugf("couldn't read osd data dir %s: %v", baseDir, err)
		return false, nil
	}
	// Do we have a block{,.wal,.db} symlink pointing to the given device? if yes
	// it's already being used as a ceph device
	for _, osdDir := range osdDirs {
		if osdDir.IsDir() {
			if !strings.HasPrefix(osdDir.Name(), "ceph-") {
				continue
			}
			for _, symlinkName := range []string{"block", "block.wal", "block.db"} {
				symlinkPath := filepath.Join(baseDir, osdDir.Name(), symlinkName)
				resolvedPath, err := filepath.EvalSymlinks(symlinkPath)
				if err == nil {
					if resolvedPath == resolved {
						logger.Debugf("device %s is used as %s for OSD %s", device, symlinkName, osdDir.Name())
						return true, nil
					}
				} else if !os.IsNotExist(err) {
					logger.Errorf("failed to resolve symlink %s: %v", symlinkPath, err)
					return false, err
				}
			}
		}
	}
	// Fall-through: no symlink found
	logger.Debugf("device %s is not used as WAL or DB device for any OSD", device)
	return false, nil
}

// IsPristineDisk checks if a block device is pristine. It uses both zero-byte checking and ceph-bluestore-tool to
// detect any existing labels or data.
func IsPristineDisk(devicePath string) (bool, error) {
	return IsPristineDiskWithFs(devicePath, afero.NewOsFs())
}

// IsPristineDiskWithFs checks if a block device is pristine using the provided filesystem.
func IsPristineDiskWithFs(devicePath string, fs afero.Fs) (bool, error) {
	logger.Infof("Start pristine disk check for device: %s", devicePath)

	// First, quick check of some likely locations for non-zero data
	logger.Debugf("Perform zero-byte check on device: %s", devicePath)
	pristineByZeroCheck, err := checkForZeros(devicePath, fs)
	if err != nil {
		logger.Errorf("Zero-byte check failed for device %s: %v", devicePath, err)
		return false, err
	}
	if !pristineByZeroCheck {
		logger.Infof("Device %s failed zero-byte check - contains non-zero data at strategic locations", devicePath)
		return false, nil
	}

	// Second, use ceph-bluestore-tool to check for any labels
	logger.Debugf("Performing ceph-bluestore-tool label check on device: %s", devicePath)
	pristineByCephTool, err := checkDeviceWithBluestoreTool(devicePath)
	if err != nil {
		// If ceph-bluestore-tool fails for any reason, treat as pristine for robustness
		logger.Infof("ceph-bluestore-tool check failed for %s, treating as pristine for robustness: %v", devicePath, err)
		return true, nil
	}

	logger.Infof("Device %s pristine", devicePath)
	return pristineByCephTool, nil
}

// checkForZeros performs lightweight zero-byte checking on some strategic locations of the device
// in 512b chunks
func checkForZeros(devicePath string, fs afero.Fs) (bool, error) {
	file, err := fs.Open(devicePath)
	if err != nil {
		logger.Errorf("failed to open device %s: %v", devicePath, err)
		return false, err
	}
	defer file.Close()

	deviceSize, err := getBlockDeviceSize(devicePath)
	if err != nil {
		logger.Errorf("failed to get block device size for %s: %v", devicePath, err)
		return false, err
	}
	logger.Debugf("Device %s size: %d bytes", devicePath, deviceSize)

	// Define locations to check (in bytes from start)
	checkPoints := []int64{
		0,       // Beginning of disk (MBR, GPT primary)
		512,     // and second sector
		1024,    // Superblock
		2048,    // GPT backup
		4096,    // Filesystem superblock
		65536,   // 64KB
		1048576, // 1MB
	}

	// Add some locations at the end of the disk
	if deviceSize > 1048576 {
		checkPoints = append(checkPoints, deviceSize-1024, deviceSize-512)
		logger.Debugf("Device %s is large enough, add end-of-disk checkpoints", devicePath)
	}

	const checkSize = 512
	buffer := make([]byte, checkSize)

	for i, offset := range checkPoints {
		if offset < 0 || offset+checkSize > deviceSize {
			logger.Debugf("Skipping checkpoint %d at offset %d (out of bounds for %s)", i+1, offset, devicePath)
			continue
		}

		logger.Debugf("Checking %d/%d at offset %d on device %s", i+1, len(checkPoints), offset, devicePath)

		_, err := file.Seek(offset, 0)
		if err != nil {
			logger.Errorf("failed to seek to %d in device %s: %v", offset, devicePath, err)
			return false, err
		}

		bytesRead, err := file.Read(buffer)
		if err != nil {
			logger.Errorf("failed to read from device %s at %d: %v", devicePath, offset, err)
			return false, err
		}

		for j := 0; j < bytesRead; j++ {
			if buffer[j] != 0x0 {
				logger.Infof("Device %s has non-zero data at offset %d (byte %d: 0x%02x), not pristine", devicePath, offset, j, buffer[j])
				return false, nil
			}
		}
		logger.Debugf("Checkpoint %d/%d at %d passed (all zeros)", i+1, len(checkPoints), offset)
	}

	logger.Debugf("All %d checkpoints passed for device %s", len(checkPoints), devicePath)
	return true, nil
}

// getBlockDeviceSize returns the size of a block device in bytes.
// It reads from sysfs; for regular files (used in tests), it falls back to stat.Size().
func getBlockDeviceSize(devicePath string) (int64, error) {
	// First resolve symlinks to get the real device path
	resolved, err := filepath.EvalSymlinks(devicePath)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve device path: %w", err)
	}

	// Stat the device to get mode and device numbers
	info, err := os.Stat(resolved)
	if err != nil {
		return 0, fmt.Errorf("failed to stat device: %w", err)
	}

	// Check if this is a block device
	if info.Mode()&os.ModeDevice != 0 {
		// Get the underlying syscall.Stat_t to access device major/minor
		stat, ok := info.Sys().(*syscall.Stat_t)
		if ok {
			// Use /sys/dev/block/<major>:<minor>/size which works for all block devices
			// including those created with mknod (custom device node names).
			major := unix.Major(stat.Rdev)
			minor := unix.Minor(stat.Rdev)
			sysfsPath := fmt.Sprintf("/sys/dev/block/%d:%d/size", major, minor)

			if data, err := os.ReadFile(sysfsPath); err == nil {
				sectors, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
				if err != nil {
					return 0, fmt.Errorf("failed to parse device size from sysfs: %w", err)
				}
				// Convert sectors (512 bytes each) to bytes
				return sectors * 512, nil
			}
		}
		// Block device but couldn't get size from sysfs
		return 0, fmt.Errorf("unable to determine block device size from sysfs")
	}

	// Regular file - use stat.Size() (used in unit tests)
	if info.Size() == 0 {
		return 0, fmt.Errorf("unable to determine device size (stat returned 0)")
	}
	return info.Size(), nil
}

// checkDeviceWithBluestoreTool uses ceph-bluestore-tool to check for existing labels
func checkDeviceWithBluestoreTool(devicePath string) (bool, error) {
	logger.Debugf("Running ceph-bluestore-tool show-label on device: %s", devicePath)

	output, err := ProcessExec.RunCommand("ceph-bluestore-tool", "show-label", "--dev", devicePath)
	if err != nil {
		// Treat all errors as "device is pristine" for this includes "no label found"
		logger.Infof("ceph-bluestore-tool check on %s returned error, likely pristine: %v", devicePath, err)
		logger.Debugf("ceph-bluestore-tool output for %s: %s", devicePath, output)
		return true, nil
	}

	// show-label succeeds --> there's an existing bluestore label
	logger.Infof("ceph-bluestore-tool found existing bluestore labels on %s (not pristine)", devicePath)
	logger.Debugf("ceph-bluestore-tool output for %s: %s", devicePath, output)
	return false, nil
}
