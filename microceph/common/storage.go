package common

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/spf13/afero"
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

// IsPristineDisk checks if a block device is pristine by reading the first 2048 bytes
// and verifying they are all zeros.
func IsPristineDisk(devicePath string) (bool, error) {
	return IsPristineDiskWithFs(devicePath, afero.NewOsFs())
}

// IsPristineDiskWithFs checks if a block device is pristine using the provided filesystem.
func IsPristineDiskWithFs(devicePath string, fs afero.Fs) (bool, error) {
	const wantBytes = 2048

	file, err := fs.Open(devicePath)
	if err != nil {
		logger.Errorf("failed to open device %s: %v", devicePath, err)
		return false, err
	}
	defer file.Close()

	data := make([]byte, wantBytes)
	readBytes, err := file.Read(data)
	if err != nil {
		logger.Errorf("failed to read from device %s: %v", devicePath, err)
		return false, err
	}

	if readBytes != wantBytes {
		logger.Warnf("short read from %s: got %d bytes, expected %d", devicePath, readBytes, wantBytes)
		return false, nil
	}

	// Check if all bytes are zero
	for _, b := range data {
		if b != 0x0 {
			return false, nil
		}
	}

	return true, nil
}
