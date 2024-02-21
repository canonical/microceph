package common

import (
	"bufio"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/constants"
	"os"
	"path/filepath"
	"strings"
)

// IsMounted checks if a device is mounted.
func IsMounted(device string) (bool, error) {
	// Resolve any symlink and get the absolute path of the device.
	// Note /proc/mounts contains the absolute path of the device as well.
	resolvedPath, err := filepath.EvalSymlinks(filepath.Join(constants.GetPathConst().RootFs, device))
	if err != nil {
		// Handle errors other than not existing differently as EvalSymlinks takes care of symlink resolution
		return false, err
	}
	file, err := os.Open(filepath.Join(constants.GetPathConst().ProcPath, "mounts"))
	if err != nil {
		return false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		// Each line in /proc/mounts is of the format:
		// device mountpoint fstype options dump pass
		// --> split the line into parts and check if the first part matches
		parts := strings.Fields(scanner.Text())
		if len(parts) > 0 && parts[0] == resolvedPath {
			return true, nil
		}
	}
	err = scanner.Err()
	if err != nil {
		return false, err
	}
	return false, nil
}

// IsCephDevice checks if a given device is used as either a WAL or DB block device in any Ceph OSD.
func IsCephDevice(device string) (bool, error) {
	// Resolve the given device path first to handle any symlinks
	resolved, err := filepath.EvalSymlinks(device)
	if err != nil {
		logger.Errorf("failed to resolve device path: %v", err)
		return false, err
	}
	// Check all ceph data dirs
	baseDir := filepath.Join(constants.GetPathConst().DataPath, "osd")
	osdDirs, err := os.ReadDir(baseDir)
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
