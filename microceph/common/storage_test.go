package common

import (
	"github.com/canonical/microceph/microceph/tests"
	"github.com/stretchr/testify/suite"
	"os"
	"path/filepath"
	"testing"
)

type StorageDeviceTestSuite struct {
	tests.BaseSuite
	devicePath string
}

func (s *StorageDeviceTestSuite) SetupTest() {
	s.BaseSuite.SetupTest()
	s.CopyCephConfigs()

	osdDir := filepath.Join(s.Tmp, "SNAP_COMMON", "data", "osd", "ceph-0")
	os.MkdirAll(osdDir, 0775)
	// create a temp file to use as a device
	s.devicePath = filepath.Join(s.Tmp, "device")
	os.Create(s.devicePath)
	os.MkdirAll(filepath.Join(s.Tmp, "dev"), 0775)
	os.Create(filepath.Join(s.Tmp, "dev", "sdb"))
	os.Create(filepath.Join(s.Tmp, "dev", "sdc"))

	// create a /proc/mounts like file
	mountsFile := filepath.Join(s.Tmp, "proc", "mounts")
	mountsContent := "/dev/root / ext4 rw,relatime,discard,errors=remount-ro 0 0\n"
	mountsContent += filepath.Join(s.Tmp, "dev", "sdb") + " /mnt ext2 rw,relatime 0 0\n"
	_ = os.WriteFile(mountsFile, []byte(mountsContent), 0644)
}

func (s *StorageDeviceTestSuite) TestIsCephDeviceNotADevice() {
	isCeph, err := IsCephDevice(s.devicePath)
	s.NoError(err, "There should not be an error when checking a device that is not used by Ceph")
	s.False(isCeph, "The device should not be identified as a Ceph device")
}

func (s *StorageDeviceTestSuite) TestIsCephDeviceHaveDevice() {
	// create a symlink to the device file
	os.Symlink(s.devicePath, filepath.Join(s.Tmp, "SNAP_COMMON", "data", "osd", "ceph-0", "block"))
	isCeph, err := IsCephDevice(s.devicePath)
	s.NoError(err, "There should not be an error when checking a device that is used by Ceph")
	s.True(isCeph, "The device should be identified as a Ceph device")
}

func (s *StorageDeviceTestSuite) TestIsMounted() {
	// we added a /proc/mounts like file containing an entry for /dev/sdb
	mounted, err := IsMounted("/dev/sdb")
	s.NoError(err, "There should not be an error when checking if a device is mounted")
	s.True(mounted, "The device should be mounted")

	mounted, err = IsMounted("/dev/sdc")
	s.NoError(err, "There should not be an error when checking if a device is not mounted")
	s.False(mounted, "The device should not be mounted")
}

func TestStorageDeviceSuite(t *testing.T) {
	suite.Run(t, new(StorageDeviceTestSuite))
}
