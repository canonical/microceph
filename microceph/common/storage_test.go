package common

import (
	"context"
	"github.com/canonical/microceph/microceph/tests"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// MockRunner implements the Runner interface for testing
type MockRunner struct {
	mock.Mock
}

func (m *MockRunner) RunCommand(name string, arg ...string) (string, error) {
	args := m.Called(append([]interface{}{name}, interfaceSlice(arg)...)...)
	return args.String(0), args.Error(1)
}

func (m *MockRunner) RunCommandContext(ctx context.Context, name string, arg ...string) (string, error) {
	args := m.Called(append([]interface{}{ctx, name}, interfaceSlice(arg)...)...)
	return args.String(0), args.Error(1)
}

// Helper function to convert []string to []interface{}
func interfaceSlice(slice []string) []interface{} {
	result := make([]interface{}, len(slice))
	for i, v := range slice {
		result[i] = v
	}
	return result
}

// createRealExitError creates a real exec.ExitError by running a command that fails
func createRealExitError(code int) error {
	var cmd *exec.Cmd
	if code == 1 {
		// Use 'false' command which always exits with code 1
		cmd = exec.Command("false")
	} else {
		// For other exit codes, use 'sh -c "exit N"'
		cmd = exec.Command("sh", "-c", "exit "+string(rune(code+'0')))
	}
	
	err := cmd.Run()
	// This will be a real *exec.ExitError with the correct exit code
	return err
}

func NewMockExitError(code int) error {
	return createRealExitError(code)
}

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
	// Test with a device that doesn't exist in the real filesystem
	// This should return an error from EvalSymlinks since the path doesn't exist
	mounted, err := IsMounted("/dev/nonexistent")
	s.Error(err, "There should be an error when checking a non-existent device")
	s.False(mounted, "A non-existent device should not be mounted")

	// Second test, need to mock ProcessExec to avoid calling the real findmnt
	originalProcessExec := ProcessExec
	defer func() { ProcessExec = originalProcessExec }()
	mockRunner := &MockRunner{}
	ProcessExec = mockRunner

	// Mock findmnt to return exit status 1 (device not mounted) using real exec.ExitError
	exitError1 := createRealExitError(1)
	mockRunner.On("RunCommand", "findmnt", "--source", mock.AnythingOfType("string")).Return("", exitError1).Once()

	devicePath := "/dev/sdb" // We have a dummy device path for testing
	mounted, err = IsMounted(devicePath)
	// Should handle exit code 1 as "not mounted" without error
	s.NoError(err, "There should not be an error when findmnt returns 'not mounted'")
	s.False(mounted, "Device should not be mounted in test environment")

	// Test device that is mounted (exit code 0)
	mockRunner.On("RunCommand", "findmnt", "--source", mock.AnythingOfType("string")).Return("", nil).Once()
	mounted, err = IsMounted(devicePath)
	s.NoError(err, "There should not be an error when findmnt returns 'mounted'")
	s.True(mounted, "Device should be mounted when findmnt returns exit code 0")
}

func TestStorageDeviceSuite(t *testing.T) {
	suite.Run(t, new(StorageDeviceTestSuite))
}
