package ceph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/tests"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/spf13/afero"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// MockPathValidator is a mock implementation of PathValidator for testing.
type MockPathValidator struct {
	mock.Mock
}

// IsBlockdevPath mocks the path validation.
func (m *MockPathValidator) IsBlockdevPath(path string) bool {
	args := m.Called(path)
	return args.Bool(0)
}

// MockMountChecker is a mock implementation of MountChecker for testing.
type MockMountChecker struct {
	mock.Mock
}

// IsMounted mocks the mount checking.
func (m *MockMountChecker) IsMounted(device string) (bool, error) {
	args := m.Called(device)
	return args.Bool(0), args.Error(1)
}

// MockFileStater is a mock implementation of FileStater for testing.
type MockFileStater struct {
	mock.Mock
}

// GetFileStat mocks the file stat operation.
func (m *MockFileStater) GetFileStat(path string) (uid int, gid int, major uint32, minor uint32, inode uint64, nlink int, err error) {
	args := m.Called(path)
	return args.Int(0), args.Int(1), args.Get(2).(uint32), args.Get(3).(uint32), args.Get(4).(uint64), args.Int(5), args.Error(6)
}

// MockPristineChecker is a mock implementation of PristineChecker for testing.
type MockPristineChecker struct {
	mock.Mock
}

// IsPristineDisk mocks the pristine disk check operation.
func (m *MockPristineChecker) IsPristineDisk(devicePath string) (bool, error) {
	args := m.Called(devicePath)
	return args.Bool(0), args.Error(1)
}

func createExitError(t *testing.T, code int) error {
	t.Helper()
	cmd := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code))
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected command to fail with exit code %d", code)
	}
	return err
}

// osdSuite is the test suite for adding OSDs.
type osdSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

// createMockDeviceEnvironment creates a mock filesystem environment with device files and proc mounts
func (s *osdSuite) createMockDeviceEnvironment(fs afero.Fs, tempDir string, deviceName string) (string, string, error) {
	// Create empty /proc/mounts
	procDir := filepath.Join(tempDir, "proc")
	err := fs.MkdirAll(procDir, 0755)
	if err != nil {
		return "", "", err
	}
	err = afero.WriteFile(fs, filepath.Join(procDir, "mounts"), []byte(""), 0644)
	if err != nil {
		return "", "", err
	}

	// Create the device file
	devDir := filepath.Join(tempDir, "dev")
	err = fs.MkdirAll(devDir, 0755)
	if err != nil {
		return "", "", err
	}
	devicePath := filepath.Join(devDir, deviceName)
	err = afero.WriteFile(fs, devicePath, []byte(""), 0644)
	if err != nil {
		return "", "", err
	}

	// Create OSD directory
	osdPath := filepath.Join(tempDir, "osd")
	err = fs.MkdirAll(osdPath, 0755)
	if err != nil {
		return "", "", err
	}

	return devicePath, osdPath, nil
}

func TestOSD(t *testing.T) {
	suite.Run(t, new(osdSuite))
}

func (s *osdSuite) TestSanityCheckMissingOSDReturnsNotFound() {
	origOSDQuery := database.OSDQuery
	mockOSDQuery := mocks.NewOSDQueryInterface(s.T())
	database.OSDQuery = mockOSDQuery
	defer func() {
		database.OSDQuery = origOSDQuery
	}()

	var st mcTypes.State
	si := mocks.NewStateInterface(s.T())
	si.On("ClusterState").Return(st).Maybe()
	mockOSDQuery.On("HaveOSD", mock.Anything, mock.Anything, int64(9999)).Return(false, nil).Once()

	err := sanityCheck(context.Background(), si, 9999)
	require.Error(s.T(), err)
	assert.True(s.T(), api.StatusErrorCheck(err, http.StatusNotFound))
	assert.EqualError(s.T(), err, "osd.9999 not found")
}

// Expect: run ceph osd crush rule ls
func addCrushRuleLsExpectations(r *mocks.Runner) {
	r.On("RunCommand", tests.CmdAny("ceph", 4)...).Return("microceph_auto_osd", nil).Once()
}

// Expect: run ceph osd crush rule dump
func addCrushRuleDumpExpectations(r *mocks.Runner) {
	json := `{ "rule_id": 77 }`

	r.On("RunCommand", tests.CmdAny("ceph", 5)...).Return(json, nil).Once()
}

// Expect: run ceph osd crush rule ls json
func addCrushRuleLsJsonExpectations(r *mocks.Runner) {
	json := `[{
        "crush_rule": 77
        "pool_name": "foopool",
    }]`
	r.On("RunCommand", tests.CmdAny("ceph", 5)...).Return(json, nil).Once()
}

// Expect: run ceph osd pool set
func addOsdPoolSetExpectations(r *mocks.Runner) {
	r.On("RunCommand", tests.CmdAny("ceph", 6)...).Return("ok", nil).Once()
}

// Expect: run ceph config set
func addSetDefaultRuleExpectations(r *mocks.Runner) {
	r.On("RunCommand", tests.CmdAny("ceph", 7)...).Return("ok", nil).Once()
}

// Expect: run ceph osd tree
func addOsdTreeExpectations(r *mocks.Runner) {
	json := `{
   "nodes" : [
      {
         "children" : [
            -4,
            -3,
            -2
         ],
         "id" : -1,
         "name" : "default",
         "type" : "root",
         "type_id" : 11
      },
      {
         "children" : [
            0
         ],
         "id" : -2,
         "name" : "m-0",
         "pool_weights" : {},
         "type" : "host",
         "type_id" : 1
      },
      {
         "crush_weight" : 0.0035858154296875,
         "depth" : 2,
         "exists" : 1,
         "id" : 0,
         "name" : "osd.0",
         "pool_weights" : {},
         "primary_affinity" : 1,
         "reweight" : 1,
         "status" : "up",
         "type" : "osd",
         "type_id" : 0
      }
  ], "stray" : [{ "id": 77,
          "name": "osd.77",
          "exists": 1} ]}`
	r.On("RunCommand", tests.CmdAny("ceph", 4)...).Return(json, nil).Once()

}

func addSetOsdStateUpExpectations(r *mocks.Runner) {
	r.On("RunCommand", "snapctl", "start", "microceph.osd", "--enable").Return("ok", nil).Once()
}

func addSetOsdStateDownExpectations(r *mocks.Runner) {
	r.On("RunCommand", "snapctl", "stop", "microceph.osd", "--disable").Return("ok", nil).Once()
}

func addSetOsdStateUpFailedExpectations(r *mocks.Runner) {
	r.On("RunCommand", "snapctl", "start", "microceph.osd", "--enable").Return("fail", fmt.Errorf("some errors")).Once()
}

func addSetOsdStateDownFailedExpectations(r *mocks.Runner) {
	r.On("RunCommand", "snapctl", "stop", "microceph.osd", "--disable").Return("fail", fmt.Errorf("some errors")).Once()
}

func addOsdtNooutFlagTrueExpectations(r *mocks.Runner) {
	r.On("RunCommand", "ceph", "osd", "set", "noout").Return("ok", nil).Once()
}

func addOsdtNooutFlagFalseExpectations(r *mocks.Runner) {
	r.On("RunCommand", "ceph", "osd", "unset", "noout").Return("ok", nil).Once()
}

func addOsdtNooutFlagFailedExpectations(r *mocks.Runner) {
	r.On("RunCommand", "ceph", "osd", "set", "noout").Return("fail", fmt.Errorf("some errors")).Once()
}

func addIsOsdNooutSetTrueExpections(r *mocks.Runner) {
	r.On("RunCommand", "ceph", "osd", "dump", "-f", "json").Return(`{"flags_set":["sortbitwise","noout"]}`, nil).Once()
}

func addIsOsdNooutSetFalseExpections(r *mocks.Runner) {
	r.On("RunCommand", "ceph", "osd", "dump", "-f", "json").Return(`{"flags_set":["sortbitwise"]}`, nil).Once()
}

func addIsOsdNooutSetFailedExpections(r *mocks.Runner) {
	r.On("RunCommand", "ceph", "osd", "dump", "-f", "json").Return("fail", fmt.Errorf("some errors")).Once()
}

func (s *osdSuite) SetupTest() {

	s.BaseSuite.SetupTest()
	s.CopyCephConfigs()

}

// TestSwitchHostFailureDomain tests the switchFailureDomain function
func (s *osdSuite) TestSwitchHostFailureDomain() {
	r := mocks.NewRunner(s.T())

	// dump crush rules to resolve names
	addCrushRuleDumpExpectations(r)
	// set default crush rule
	addSetDefaultRuleExpectations(r)
	// list to check if crush rule exists
	addCrushRuleLsExpectations(r)
	// dump crush rules to resolve names
	addCrushRuleDumpExpectations(r)
	// list pools
	addCrushRuleLsJsonExpectations(r)
	// set pool crush rule
	addOsdPoolSetExpectations(r)

	common.ProcessExec = r

	mgr := NewOSDManager(nil)
	mgr.fs = afero.NewMemMapFs()
	err := mgr.switchFailureDomain("osd", "host")
	assert.NoError(s.T(), err)
}

// TestUpdateFailureDomain tests the updateFailureDomain function
func (s *osdSuite) TestUpdateFailureDomain() {
	// Mock getAZData to return no AZs so the test proceeds to member count check.
	defer withMockAZData(azData{})()

	u := api.NewURL()
	state := &mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
	}

	r := mocks.NewRunner(s.T())

	// dump crush rules to resolve names
	addCrushRuleDumpExpectations(r)
	// set default crush rule
	addSetDefaultRuleExpectations(r)
	// list to check if crush rule exists
	addCrushRuleLsExpectations(r)
	// dump crush rules to resolve names
	addCrushRuleDumpExpectations(r)
	// list pools
	addCrushRuleLsJsonExpectations(r)
	// set pool crush rule
	addOsdPoolSetExpectations(r)

	common.ProcessExec = r

	c := mocks.NewMemberCounterInterface(s.T())
	c.On("Count", mock.Anything).Return(3, nil).Once()
	database.MemberCounter = c

	s.TestStateInterface = mocks.NewStateInterface(s.T())
	s.TestStateInterface.On("ClusterState").Return(state).Maybe()

	mgr := NewOSDManager(s.TestStateInterface.ClusterState())
	mgr.fs = afero.NewMemMapFs()
	err := mgr.updateFailureDomain(context.Background(), s.TestStateInterface.ClusterState())
	assert.NoError(s.T(), err)

}

// TestHaveOSDInCeph tests the haveOSDInCeph function
func (s *osdSuite) TestHaveOSDInCeph() {
	r := mocks.NewRunner(s.T())
	// add osd tree expectations
	addOsdTreeExpectations(r)
	addOsdTreeExpectations(r)

	common.ProcessExec = r

	mgr := NewOSDManager(nil)
	mgr.runner = r

	res, err := mgr.haveOSDInCeph(0)
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), res, true)

	res, err = mgr.haveOSDInCeph(77)
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), res, false)

}

// TestSetOsdStateOkay tests the SetOsdState function when no error occurs
func (s *osdSuite) TestSetOsdStateOkay() {
	r := mocks.NewRunner(s.T())
	addSetOsdStateUpExpectations(r)
	addSetOsdStateDownExpectations(r)

	// patch ProcessExec
	common.ProcessExec = r

	err := SetOsdState(true)
	assert.NoError(s.T(), err)

	err = SetOsdState(false)
	assert.NoError(s.T(), err)
}

// TestSetOsdStateFail tests the SetOsdState function when error occurs
func (s *osdSuite) TestSetOsdStateFail() {
	r := mocks.NewRunner(s.T())
	addSetOsdStateUpFailedExpectations(r)
	addSetOsdStateDownFailedExpectations(r)

	// patch ProcessExec
	common.ProcessExec = r

	err := SetOsdState(true)
	assert.Error(s.T(), err)

	err = SetOsdState(false)
	assert.Error(s.T(), err)
}

// TestSetOsdNooutFlagOkay tests the setOsdNooutFlag function when no error occurs
func (s *osdSuite) TestSetOsdNooutFlagOkay() {
	r := mocks.NewRunner(s.T())
	addOsdtNooutFlagTrueExpectations(r)
	addOsdtNooutFlagFalseExpectations(r)

	// patch ProcessExec
	common.ProcessExec = r

	err := setOsdNooutFlag(true)
	assert.NoError(s.T(), err)

	err = setOsdNooutFlag(false)
	assert.NoError(s.T(), err)
}

// TestSetOsdNooutFlagFail tests the setOsdNooutFlag function when error occurs
func (s *osdSuite) TestSetOsdNooutFlagFail() {
	r := mocks.NewRunner(s.T())
	addOsdtNooutFlagFailedExpectations(r)

	// patch ProcessExec
	common.ProcessExec = r

	err := setOsdNooutFlag(true)
	assert.Error(s.T(), err)
}

// TestIsOsdNooutSetOkay tests the isOsdNooutSet function when no error occurs
func (s *osdSuite) TestIsOsdNooutSetOkay() {
	r := mocks.NewRunner(s.T())
	addIsOsdNooutSetTrueExpections(r)
	addIsOsdNooutSetFalseExpections(r)

	// patch ProcessExec
	common.ProcessExec = r

	// noout is set
	set, err := isOsdNooutSet()
	assert.True(s.T(), set)
	assert.NoError(s.T(), err)

	// noout is not set
	set, err = isOsdNooutSet()
	assert.False(s.T(), set)
	assert.NoError(s.T(), err)
}

// TestIsOsdNooutSetFail tests the isOsdNooutSet function when error occurs
func (s *osdSuite) TestIsOsdNooutSetFail() {
	r := mocks.NewRunner(s.T())
	addIsOsdNooutSetFailedExpections(r)

	// patch ProcessExec
	common.ProcessExec = r

	// error running ceph osd dump
	set, err := isOsdNooutSet()
	assert.False(s.T(), set)
	assert.Error(s.T(), err)
}

// TestAddBulkDisksValidation ensures batch addition arguments are checked.
func (s *osdSuite) TestAddBulkDisksValidation() {
	osdmgr := NewOSDManager(nil)
	osdmgr.fs = afero.NewMemMapFs()
	disks := []types.DiskParameter{
		{Path: "/dev/sdx"},
		{Path: "/dev/sdy"},
	}
	wal := &types.DiskParameter{Path: "/dev/wal"}

	resp := osdmgr.addBulkDisks(context.Background(), disks, wal, nil)
	assert.NotEmpty(s.T(), resp.ValidationError)
	assert.Equal(s.T(), "Failure", resp.Reports[0].Report)
}

// TestNewOSDManager tests the OSDManager constructor
func (s *osdSuite) TestNewOSDManager() {
	state := &mocks.MockState{}
	osdmgr := NewOSDManager(state)
	assert.NotNil(s.T(), osdmgr)
	assert.Equal(s.T(), state, osdmgr.state)
	assert.NotNil(s.T(), osdmgr.runner)
	assert.NotNil(s.T(), osdmgr.fs)
}

// TestSetStablePath tests device path stabilization
func (s *osdSuite) TestSetStablePath() {
	// Create a custom OSD manager with mocked components
	osdmgr := &OSDManager{
		fs:         afero.NewMemMapFs(),
		validator:  &MockPathValidator{},
		fileStater: &MockFileStater{},
	}

	mockValidator := osdmgr.validator.(*MockPathValidator)
	mockFileStater := osdmgr.fileStater.(*MockFileStater)

	// Create mock storage with disk info
	storage := &api.ResourcesStorage{
		Disks: []api.ResourcesStorageDisk{
			{
				Device:     "8:0",
				DeviceID:   "test-disk-id",
				DevicePath: "pci-0000:00:1f.2-ata-1",
			},
		},
	}

	// Test with invalid device path (not a block device)
	param := &types.DiskParameter{Path: "/invalid/path"}
	mockValidator.On("IsBlockdevPath", "/invalid/path").Return(false).Once()
	err := osdmgr.setStablePath(storage, param)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "invalid disk path")

	// Test with valid device path
	param2 := &types.DiskParameter{Path: "/dev/sdx"}
	mockValidator.On("IsBlockdevPath", "/dev/sdx").Return(true).Once()
	mockFileStater.On("GetFileStat", "/dev/sdx").Return(0, 0, uint32(8), uint32(0), uint64(0), 0, nil).Once()
	err = osdmgr.setStablePath(storage, param2)
	assert.NoError(s.T(), err)
}

// TestStabilizeDevicePathSuccess tests successful device path stabilization
func (s *osdSuite) TestStabilizeDevicePathSuccess() {
	osdmgr := NewOSDManager(nil)
	osdmgr.fs = afero.NewMemMapFs()

	// Mock storage interface
	mockStorage := mocks.NewStorageInterface(s.T())
	osdmgr.storage = mockStorage

	// Mock validator and file stater
	mockValidator := &MockPathValidator{}
	osdmgr.validator = mockValidator
	mockFileStater := &MockFileStater{}
	osdmgr.fileStater = mockFileStater

	expectedStorage := &api.ResourcesStorage{
		Disks: []api.ResourcesStorageDisk{
			{
				Device:     "8:0",
				DeviceID:   "test-disk-id",
				DevicePath: "pci-0000:00:1f.2-ata-1",
			},
		},
	}
	mockStorage.On("GetStorage").Return(expectedStorage, nil).Once()
	mockValidator.On("IsBlockdevPath", "/dev/sdx").Return(true).Once()
	mockFileStater.On("GetFileStat", "/dev/sdx").Return(0, 0, uint32(8), uint32(0), uint64(0), 0, nil).Once()

	physParam := &types.DiskParameter{Path: "/dev/sdx"}
	storage, err := osdmgr.stabilizeDevicePath(physParam)

	// Should succeed now with mocked validation
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), expectedStorage, storage)
}

// TestPrepareDisk tests disk preparation (block, WAL, and wiping)
func (s *osdSuite) TestPrepareDisk() {
	osdmgr := NewOSDManager(nil)
	// Use OsFs for symlink support, but in a temp directory
	osdmgr.fs = afero.NewOsFs()

	// Mock validator and mount checker for tests
	mockValidator := &MockPathValidator{}
	osdmgr.validator = mockValidator
	mockMountChecker := &MockMountChecker{}
	osdmgr.mountChecker = mockMountChecker

	// Mock runner for wipe operations
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	// Create separate temp directories for each test case to avoid conflicts
	tempDir1 := s.T().TempDir()
	tempDir2 := s.T().TempDir()
	tempDir3 := s.T().TempDir()

	// Test without wipe or encrypt
	devicePath, osdPath, err := s.createMockDeviceEnvironment(osdmgr.fs, tempDir1, "sdx")
	assert.NoError(s.T(), err)
	disk := &types.DiskParameter{Path: devicePath}
	// Ab/use MockValidator to return false (not a block device) to skip mount check
	mockValidator.On("IsBlockdevPath", devicePath).Return(false).Once()
	err = osdmgr.prepareDisk(disk, "", osdPath, 0)
	assert.NoError(s.T(), err)

	// Verify symlink was created for data device
	linkPath := filepath.Join(osdPath, "block")
	exists, err := afero.Exists(osdmgr.fs, linkPath)
	assert.NoError(s.T(), err)
	assert.True(s.T(), exists)

	// Test WAL device (suffix != "", no symlink expected)
	walDevicePath, walOsdPath, err := s.createMockDeviceEnvironment(osdmgr.fs, tempDir2, "sdy")
	assert.NoError(s.T(), err)
	walDisk := &types.DiskParameter{Path: walDevicePath}
	// Again mock validator to return false (not a block device) to skip mount check
	mockValidator.On("IsBlockdevPath", walDevicePath).Return(false).Once()
	err = osdmgr.prepareDisk(walDisk, ".wal", walOsdPath, 1)
	assert.NoError(s.T(), err)

	// Test with wipe - use a different temp directory to avoid symlink conflicts
	wipeDevicePath, wipeOsdPath, err := s.createMockDeviceEnvironment(osdmgr.fs, tempDir3, "sdz")
	assert.NoError(s.T(), err)
	r.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", wipeDevicePath, "--yes-i-really-really-mean-it").Return("", nil).Once()
	wipeDisk := &types.DiskParameter{Path: wipeDevicePath, Wipe: true}
	// As above mock validator to return false
	mockValidator.On("IsBlockdevPath", wipeDevicePath).Return(false).Once()
	err = osdmgr.prepareDisk(wipeDisk, "", wipeOsdPath, 2)
	assert.NoError(s.T(), err)
}

// TestPrepareDiskMountedDevice tests that prepareDisk fails when device is mounted
func (s *osdSuite) TestPrepareDiskMountedDevice() {
	osdmgr := NewOSDManager(nil)

	mockValidator := &MockPathValidator{}
	osdmgr.validator = mockValidator

	// Mock mount checker to return true (device is mounted)
	mockMountChecker := &MockMountChecker{}
	osdmgr.mountChecker = mockMountChecker

	// Use OsFs instead of mem fs for symlink support
	osdmgr.fs = afero.NewOsFs()

	// Create temp directory for test
	tempDir := s.T().TempDir()

	// Create mock device environment
	devicePath, osdPath, err := s.createMockDeviceEnvironment(osdmgr.fs, tempDir, "sdx")
	assert.NoError(s.T(), err)

	disk := &types.DiskParameter{Path: devicePath}
	// Mocks to simulate a mounted device block device
	mockValidator.On("IsBlockdevPath", devicePath).Return(true).Once()
	mockMountChecker.On("IsMounted", devicePath).Return(true, nil).Once()

	err = osdmgr.prepareDisk(disk, "", osdPath, 0)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), fmt.Sprintf("device %s is currently mounted", devicePath))
}

// TestPrepareDiskNotMountedDevice tests that prepareDisk succeeds when device is not mounted
func (s *osdSuite) TestPrepareDiskNotMountedDevice() {
	mgr := NewOSDManager(nil)

	mockValidator := &MockPathValidator{}
	mgr.validator = mockValidator
	mockMountChecker := &MockMountChecker{}
	mgr.mountChecker = mockMountChecker

	// Use OsFs for symlink support, but in a temp directory
	mgr.fs = afero.NewOsFs()

	// Create temp directory for test
	tempDir := s.T().TempDir()

	// Create mock device environment
	devicePath, osdPath, err := s.createMockDeviceEnvironment(mgr.fs, tempDir, "sdx")
	assert.NoError(s.T(), err)

	disk := &types.DiskParameter{Path: devicePath}
	// Simulate an unmounted block device
	mockValidator.On("IsBlockdevPath", devicePath).Return(true).Once()
	mockMountChecker.On("IsMounted", devicePath).Return(false, nil).Once()

	err = mgr.prepareDisk(disk, "", osdPath, 0)
	assert.NoError(s.T(), err)

	// Verify symlink was created for data device (suffix == "")
	linkPath := filepath.Join(osdPath, "block")
	exists, err := afero.Exists(mgr.fs, linkPath)
	assert.NoError(s.T(), err)
	assert.True(s.T(), exists)
}

// TestPrepareDiskNonBlockDevice tests that mount check is skipped for non-block devices
func (s *osdSuite) TestPrepareDiskNonBlockDevice() {
	osdmgr := NewOSDManager(nil)

	// Use OsFs for symlink support
	osdmgr.fs = afero.NewOsFs()

	// Mock validator to return false (not a block device)
	mockValidator := &MockPathValidator{}
	osdmgr.validator = mockValidator

	// Mock mount checker (won't be called since it's not a block device)
	mockMountChecker := &MockMountChecker{}
	osdmgr.mountChecker = mockMountChecker

	// Create temp directory for test
	tempDir := s.T().TempDir()

	// Create mock device environment
	devicePath, osdPath, err := s.createMockDeviceEnvironment(osdmgr.fs, tempDir, "some-file")
	assert.NoError(s.T(), err)

	disk := &types.DiskParameter{Path: devicePath}
	mockValidator.On("IsBlockdevPath", devicePath).Return(false).Once()

	// Should not check mount status and proceed
	err = osdmgr.prepareDisk(disk, "", osdPath, 0)
	assert.NoError(s.T(), err)

	// Verify symlink was created for data device (suffix == "")
	linkPath := filepath.Join(osdPath, "block")
	exists, err := afero.Exists(osdmgr.fs, linkPath)
	assert.NoError(s.T(), err)
	assert.True(s.T(), exists)
}

// TestCheckDeviceHasPartitions tests partition detection
func (s *osdSuite) TestCheckDeviceHasPartitions() {
	// Create a custom OSD manager with mocked components
	osdmgr := &OSDManager{
		fs:         afero.NewMemMapFs(),
		validator:  &MockPathValidator{},
		fileStater: &MockFileStater{},
	}

	mockValidator := osdmgr.validator.(*MockPathValidator)
	mockFileStater := osdmgr.fileStater.(*MockFileStater)

	// Test with non-block device (should return false)
	mockValidator.On("IsBlockdevPath", "/some/file").Return(false).Once()
	storage := &api.ResourcesStorage{}
	hasPartitions, err := osdmgr.checkDeviceHasPartitions(storage, "/some/file")
	assert.NoError(s.T(), err)
	assert.False(s.T(), hasPartitions)

	// Test with block device that has no partitions
	mockValidator.On("IsBlockdevPath", "/dev/sdx").Return(true).Once()
	mockFileStater.On("GetFileStat", "/dev/sdx").Return(0, 0, uint32(8), uint32(0), uint64(0), 0, nil).Once()
	storage = &api.ResourcesStorage{
		Disks: []api.ResourcesStorageDisk{
			{
				Device:     "8:0",
				DeviceID:   "test-disk-id",
				DevicePath: "pci-0000:00:1f.2-ata-1",
				Partitions: []api.ResourcesStorageDiskPartition{}, // No partitions
			},
		},
	}
	hasPartitions, err = osdmgr.checkDeviceHasPartitions(storage, "/dev/sdx")
	assert.NoError(s.T(), err)
	assert.False(s.T(), hasPartitions)

	// Test with block device that has partitions
	mockValidator.On("IsBlockdevPath", "/dev/sdy").Return(true).Once()
	mockFileStater.On("GetFileStat", "/dev/sdy").Return(0, 0, uint32(8), uint32(16), uint64(0), 0, nil).Once()
	storage = &api.ResourcesStorage{
		Disks: []api.ResourcesStorageDisk{
			{
				Device:     "8:16",
				DeviceID:   "test-disk-id-2",
				DevicePath: "pci-0000:00:1f.2-ata-2",
				Partitions: []api.ResourcesStorageDiskPartition{
					{Device: "8:17", Partition: 1},
					{Device: "8:18", Partition: 2},
				},
			},
		},
	}
	hasPartitions, err = osdmgr.checkDeviceHasPartitions(storage, "/dev/sdy")
	assert.NoError(s.T(), err)
	assert.True(s.T(), hasPartitions)

	// Test with device not found in storage
	mockValidator.On("IsBlockdevPath", "/dev/sdz").Return(true).Once()
	mockFileStater.On("GetFileStat", "/dev/sdz").Return(0, 0, uint32(8), uint32(32), uint64(0), 0, nil).Once()
	storage = &api.ResourcesStorage{
		Disks: []api.ResourcesStorageDisk{}, // Empty disks list
	}
	hasPartitions, err = osdmgr.checkDeviceHasPartitions(storage, "/dev/sdz")
	assert.NoError(s.T(), err)
	assert.False(s.T(), hasPartitions)
}

// TestCheckEncryptSupport tests encryption support validation
func (s *osdSuite) TestCheckEncryptSupport() {
	osdmgr := NewOSDManager(nil)
	fs := afero.NewMemMapFs()
	osdmgr.fs = fs

	// Mock the ProcessExec for isIntfConnected calls
	r := mocks.NewRunner(s.T())
	originalProcessExec := common.ProcessExec
	common.ProcessExec = r
	defer func() { common.ProcessExec = originalProcessExec }()

	// Test missing /dev/mapper/control
	err := osdmgr.CheckEncryptSupport()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "missing /dev/mapper/control")

	// Create /dev/mapper/control
	err = fs.MkdirAll("/dev/mapper", 0755)
	assert.NoError(s.T(), err)
	err = afero.WriteFile(fs, "/dev/mapper/control", []byte(""), 0644)
	assert.NoError(s.T(), err)

	// Mock interface check to return false (not connected)
	r.On("RunCommand", "snapctl", "is-connected", "dm-crypt").Return("", fmt.Errorf("not connected")).Once()

	// Test dm-crypt interface not connected
	err = osdmgr.CheckEncryptSupport()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "dm-crypt interface")

	// Mock interface check to return true (connected)
	r.On("RunCommand", "snapctl", "is-connected", "dm-crypt").Return("", nil).Once()

	// Test missing dm_crypt module
	err = osdmgr.CheckEncryptSupport()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "missing dm_crypt module")

	// Create dm_crypt module
	err = fs.MkdirAll("/sys/module/dm_crypt", 0755)
	assert.NoError(s.T(), err)

	// Mock interface check again for final test
	r.On("RunCommand", "snapctl", "is-connected", "dm-crypt").Return("", nil).Once()

	// Test successful encryption support check, need /run directory
	err = fs.MkdirAll("/run", 0755)
	assert.NoError(s.T(), err)
	err = osdmgr.CheckEncryptSupport()
	assert.NoError(s.T(), err)
}

// TestTimeoutWipe tests device wiping with timeout
func (s *osdSuite) TestTimeoutWipe() {
	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	// Test successful wipe
	r.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", "/dev/sda", "--yes-i-really-really-mean-it").Return("", nil).Once()
	err := osdmgr.timeoutWipe("/dev/sda")
	assert.NoError(s.T(), err)

	// Test failed wipe
	r.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", "/dev/sdb", "--yes-i-really-really-mean-it").Return("", fmt.Errorf("wipe failed")).Once()
	err = osdmgr.timeoutWipe("/dev/sdb")
	assert.Error(s.T(), err)
}

// TestWipeDevice tests device wiping with retry logic
func (s *osdSuite) TestWipeDevice() {
	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	// Test successful wipe on first try
	r.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", "/dev/sda", "--yes-i-really-really-mean-it").Return("", nil).Once()
	osdmgr.wipeDevice(context.Background(), "/dev/sda")

	// Test wipe that succeeds after retries
	r.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", "/dev/sdb", "--yes-i-really-really-mean-it").Return("", fmt.Errorf("busy")).Once()
	r.On("RunCommandContext", mock.Anything, "ceph-bluestore-tool", "zap-device", "--dev", "/dev/sdb", "--yes-i-really-really-mean-it").Return("", nil).Once()
	osdmgr.wipeDevice(context.Background(), "/dev/sdb")
}

// TestKillOSD tests OSD process termination.
func (s *osdSuite) TestKillOSD() {
	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	originalGrace := osdKillGracePeriod
	originalForceGrace := osdKillForceGracePeriod
	originalPoll := osdKillPollInterval
	osdKillGracePeriod = 20 * time.Millisecond
	osdKillForceGracePeriod = 20 * time.Millisecond
	osdKillPollInterval = 5 * time.Millisecond
	s.T().Cleanup(func() {
		osdKillGracePeriod = originalGrace
		osdKillForceGracePeriod = originalForceGrace
		osdKillPollInterval = originalPoll
	})

	// Test successful kill with graceful exit.
	r.On("RunCommand", "pkill", "-f", "ceph-osd .* --id 0$").Return("", nil).Once()
	r.On("RunCommand", "pgrep", "-f", "ceph-osd .* --id 0$").Return("", createExitError(s.T(), 1)).Once()
	err := osdmgr.killOSD(0)
	assert.NoError(s.T(), err)

	// Test escalation to SIGKILL when SIGTERM does not stop the process.
	osdKillGracePeriod = 0
	r.On("RunCommand", "pkill", "-f", "ceph-osd .* --id 2$").Return("", nil).Once()
	r.On("RunCommand", "pgrep", "-f", "ceph-osd .* --id 2$").Return("1234", nil).Once()
	r.On("RunCommand", "pkill", "-9", "-f", "ceph-osd .* --id 2$").Return("", nil).Once()
	r.On("RunCommand", "pgrep", "-f", "ceph-osd .* --id 2$").Return("", createExitError(s.T(), 1)).Once()
	err = osdmgr.killOSD(2)
	assert.NoError(s.T(), err)
	osdKillGracePeriod = 20 * time.Millisecond

	// Test already-stopped OSD.
	r.On("RunCommand", "pkill", "-f", "ceph-osd .* --id 1$").Return("", createExitError(s.T(), 1)).Once()
	err = osdmgr.killOSD(1)
	assert.NoError(s.T(), err)

	// Test failed kill.
	r.On("RunCommand", "pkill", "-f", "ceph-osd .* --id 3$").Return("", fmt.Errorf("pkill failed")).Once()
	err = osdmgr.killOSD(3)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "failed to kill osd.3")
}

func (s *osdSuite) TestSuppressAndRestoreOSDAutostart() {
	osdmgr := NewOSDManager(nil)
	osdmgr.fs = afero.NewMemMapFs()

	osdDataPath := getOSDDataPath(7)
	require.NoError(s.T(), osdmgr.fs.MkdirAll(osdDataPath, 0700))
	require.NoError(s.T(), afero.WriteFile(osdmgr.fs, osdReadyMarkerPath(osdDataPath), []byte(""), 0600))

	restore, suppressed, err := osdmgr.suppressOSDAutostart(7)
	require.NoError(s.T(), err)
	assert.True(s.T(), suppressed)
	_, err = osdmgr.fs.Stat(osdReadyMarkerPath(osdDataPath))
	assert.True(s.T(), os.IsNotExist(err))
	_, err = osdmgr.fs.Stat(osdSuppressedReadyMarkerPath(osdDataPath))
	assert.NoError(s.T(), err)

	require.NoError(s.T(), restore())
	_, err = osdmgr.fs.Stat(osdReadyMarkerPath(osdDataPath))
	assert.NoError(s.T(), err)
	_, err = osdmgr.fs.Stat(osdSuppressedReadyMarkerPath(osdDataPath))
	assert.True(s.T(), os.IsNotExist(err))
}

func (s *osdSuite) TestSuppressOSDAutostartWithoutReadyMarker() {
	osdmgr := NewOSDManager(nil)
	osdmgr.fs = afero.NewMemMapFs()

	restore, suppressed, err := osdmgr.suppressOSDAutostart(8)
	require.NoError(s.T(), err)
	assert.False(s.T(), suppressed)
	require.NoError(s.T(), restore())
}

// notDownErr is the error ceph osd purge fails with while the monitors still have the OSD up.
func notDownErr(osd int64) error {
	return fmt.Errorf("Failed to run: ceph osd purge osd.%d --yes-i-really-mean-it: exit status 16 (Error EBUSY: osd.%d is not `down`.)", osd, osd)
}

// ranCommands returns the commands the mock runner was asked to run, in order.
func ranCommands(r *mocks.Runner) []string {
	cmds := []string{}
	for _, call := range r.Calls {
		args := make([]string, 0, len(call.Arguments))
		for _, arg := range call.Arguments {
			args = append(args, fmt.Sprint(arg))
		}
		cmds = append(cmds, strings.Join(args, " "))
	}
	return cmds
}

// ranCommandsFrom returns the commands the mock runner was asked to run, in order, starting at
// the first one equal to first.
func ranCommandsFrom(r *mocks.Runner, first string) []string {
	cmds := ranCommands(r)
	for i, cmd := range cmds {
		if cmd == first {
			return cmds[i:]
		}
	}
	return []string{}
}

// recordPurgeSleeps replaces the sleep between purge attempts with one that returns at once and
// records how long it was asked to sleep.
func (s *osdSuite) recordPurgeSleeps() *[]time.Duration {
	sleeps := &[]time.Duration{}
	origSleep := purgeRetrySleepFunc
	purgeRetrySleepFunc = func(d time.Duration) { *sleeps = append(*sleeps, d) }
	s.T().Cleanup(func() { purgeRetrySleepFunc = origSleep })
	return sleeps
}

// TestPurgeOSD tests purgeOSD retry logic.
func (s *osdSuite) TestPurgeOSD() {
	s.recordPurgeSleeps()

	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	// Fails transiently then succeeds — retry must fire regardless of error type.
	r.On("RunCommand", "ceph", "osd", "purge", "osd.1", "--yes-i-really-mean-it").Return("", fmt.Errorf("exit status 1")).Once()
	r.On("RunCommand", "ceph", "osd", "purge", "osd.1", "--yes-i-really-mean-it").Return("", nil).Once()
	r.On("RunCommand", "ceph", "osd", "ls", "-f", "json").Return("[0,2]", nil).Once()
	err := osdmgr.purgeOSD(1)
	assert.NoError(s.T(), err)

	// Only the purge failures that said the OSD is up lead to another down, and none of these did.
	for _, cmd := range ranCommands(r) {
		assert.NotContains(s.T(), cmd, "osd down")
	}
}

// TestPurgeOSDNotDownAfterEveryAttempt tests that a monitor that never accepts the purge makes
// purgeOSD fail with that error after ten attempts, marking the OSD down before each retry but
// not after the last attempt, and that it does not sleep after the last attempt either.
func (s *osdSuite) TestPurgeOSDNotDownAfterEveryAttempt() {
	sleeps := s.recordPurgeSleeps()

	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	r.On("RunCommand", "ceph", "osd", "purge", "osd.4", "--yes-i-really-mean-it").Return("", notDownErr(4)).Times(10)
	r.On("RunCommand", "ceph", "osd", "down", "osd.4").Return("", nil).Times(9)

	err := osdmgr.purgeOSD(4)
	assert.ErrorContains(s.T(), err, "failed to purge osd.4")
	assert.ErrorContains(s.T(), err, "is not `down`")

	cmds := ranCommands(r)
	require.Len(s.T(), cmds, 19)
	assert.Equal(s.T(), "ceph osd purge osd.4 --yes-i-really-mean-it", cmds[18], "the last attempt is not followed by another down")

	// Nine sleeps between ten attempts; none after the last attempt.
	require.Len(s.T(), *sleeps, 9)
}

// TestPurgeOSDRepeatsWhenIdReappears tests that an id that is back in the OSD map after the purge
// is marked down and purged once more.
func (s *osdSuite) TestPurgeOSDRepeatsWhenIdReappears() {
	s.recordPurgeSleeps()

	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	r.On("RunCommand", "ceph", "osd", "purge", "osd.5", "--yes-i-really-mean-it").Return("", nil).Twice()
	r.On("RunCommand", "ceph", "osd", "ls", "-f", "json").Return("[0,5]", nil).Once()
	r.On("RunCommand", "ceph", "osd", "down", "osd.5").Return("", nil).Once()
	r.On("RunCommand", "ceph", "osd", "ls", "-f", "json").Return("[0]", nil).Once()

	err := osdmgr.purgeOSD(5)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), []string{
		"ceph osd purge osd.5 --yes-i-really-mean-it",
		"ceph osd ls -f json",
		"ceph osd down osd.5",
		"ceph osd purge osd.5 --yes-i-really-mean-it",
		"ceph osd ls -f json",
	}, ranCommands(r))
}

// TestPurgeOSDFailsWhenIdKeepsReappearing checks that persistent reappearance fails removal.
func (s *osdSuite) TestPurgeOSDFailsWhenIdKeepsReappearing() {
	s.recordPurgeSleeps()

	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	r.On("RunCommand", "ceph", "osd", "purge", "osd.5", "--yes-i-really-mean-it").Return("", nil).Twice()
	r.On("RunCommand", "ceph", "osd", "ls", "-f", "json").Return("[0,5]", nil).Twice()
	r.On("RunCommand", "ceph", "osd", "down", "osd.5").Return("", nil).Once()

	err := osdmgr.purgeOSD(5)
	assert.ErrorContains(s.T(), err, "still in the OSD map")
}

// TestPurgeOSDFailsWhenPurgeCannotBeVerified tests that a purge whose result cannot be read back
// is not reported as a success.
func (s *osdSuite) TestPurgeOSDFailsWhenPurgeCannotBeVerified() {
	s.recordPurgeSleeps()

	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	r.On("RunCommand", "ceph", "osd", "purge", "osd.6", "--yes-i-really-mean-it").Return("", nil).Once()
	r.On("RunCommand", "ceph", "osd", "ls", "-f", "json").Return("", fmt.Errorf("monitors unreachable")).Once()

	err := osdmgr.purgeOSD(6)
	assert.ErrorContains(s.T(), err, "failed to check that osd.6 was purged")
}

// removeOSDMocks is what doRemoveOSD talks to, see setupRemoveOSD.
type removeOSDMocks struct {
	runner   *mocks.Runner
	osdQuery *mocks.OSDQueryInterface
	state    *mocks.StateInterface
}

// setupRemoveOSD sets up what doRemoveOSD needs to remove osd with the safety checks bypassed: the
// OSD is in the database, its data directory has a ready marker, the default CRUSH rule is not the
// host rule so there is no failure domain to scale down, and purge retries do not sleep. The tests
// add the expectations for the Ceph steps they run.
func (s *osdSuite) setupRemoveOSD(osd int64) removeOSDMocks {
	t := s.T()
	s.recordPurgeSleeps()

	m := removeOSDMocks{
		runner:   mocks.NewRunner(t),
		osdQuery: mocks.NewOSDQueryInterface(t),
		state:    mocks.NewStateInterface(t),
	}
	origExec := common.ProcessExec
	origQuery := database.OSDQuery
	common.ProcessExec = m.runner
	database.OSDQuery = m.osdQuery
	t.Cleanup(func() {
		common.ProcessExec = origExec
		database.OSDQuery = origQuery
	})

	var st mcTypes.State
	m.state.On("ClusterState").Return(st).Maybe()
	m.osdQuery.On("HaveOSD", mock.Anything, mock.Anything, osd).Return(true, nil).Once()
	m.runner.On("RunCommand", "ceph", "config", "get", "mon", "osd_pool_default_crush_rule").Return("1\n", nil).Once()
	m.runner.On("RunCommand", "ceph", "osd", "crush", "rule", "dump", "microceph_auto_host").Return(`{"rule_id": 2}`, nil).Once()

	osdDataPath := getOSDDataPath(osd)
	require.NoError(t, os.MkdirAll(osdDataPath, 0700))
	require.NoError(t, os.WriteFile(osdReadyMarkerPath(osdDataPath), nil, 0600))
	return m
}

// expectInCrushTree expects the OSD to be found in the CRUSH tree, drained and taken out.
func (m removeOSDMocks) expectInCrushTree(osd int64) {
	tree := fmt.Sprintf(`{"nodes":[{"id":%d,"type":"osd"}]}`, osd)
	m.runner.On("RunCommand", "ceph", "osd", "tree", "-f", "json").Return(tree, nil).Once()
	m.runner.On("RunCommand", "ceph", "osd", "crush", "reweight", fmt.Sprintf("osd.%d", osd), "0.000000").Return("", nil).Once()
	m.runner.On("RunCommand", "ceph", "osd", "out", fmt.Sprintf("osd.%d", osd)).Return("", nil).Once()
}

// whenOSDStops returns what runs when the OSD process is stopped: it checks that autostart is
// suppressed by then. The shared osd service would respawn the OSD otherwise and undo the down that
// follows.
func whenOSDStops(t *testing.T, osd int64) func(mock.Arguments) {
	return func(mock.Arguments) {
		osdDataPath := getOSDDataPath(osd)
		assert.NoFileExists(t, osdReadyMarkerPath(osdDataPath), "autostart is not suppressed when the OSD process is stopped")
		assert.FileExists(t, osdSuppressedReadyMarkerPath(osdDataPath), "autostart is not suppressed when the OSD process is stopped")
	}
}

// expectOSDProcessStopped verifies that the OSD is gone after autostart was suppressed.
func expectOSDProcessStopped(t *testing.T, r *mocks.Runner, osd int64) {
	cmdline := fmt.Sprintf("ceph-osd .* --id %d$", osd)
	r.On("RunCommand", "pkill", "-f", cmdline).Run(whenOSDStops(t, osd)).Return("", nil).Once()
	r.On("RunCommand", "pgrep", "-f", cmdline).Return("", createExitError(t, 1)).Once()
}

// expectNoOSDProcess expects killOSD to find no OSD process to stop, as when the daemon died.
func expectNoOSDProcess(t *testing.T, r *mocks.Runner, osd int64) {
	r.On("RunCommand", "pkill", "-f", fmt.Sprintf("ceph-osd .* --id %d$", osd)).Run(whenOSDStops(t, osd)).Return("", createExitError(t, 1)).Once()
}

// expectOSDStopFails expects killOSD to fail to stop the OSD process with err.
func expectOSDStopFails(t *testing.T, r *mocks.Runner, osd int64, err error) {
	r.On("RunCommand", "pkill", "-f", fmt.Sprintf("ceph-osd .* --id %d$", osd)).Run(whenOSDStops(t, osd)).Return("", err).Once()
}

// expectRecordRemoved expects the primary storage of the OSD to be cleared and its record deleted.
func (s *osdSuite) expectRecordRemoved(m removeOSDMocks, osd int64) {
	backing := filepath.Join(s.Tmp, fmt.Sprintf("osd-%d-backing", osd))
	require.NoError(s.T(), os.WriteFile(backing, nil, 0600))
	m.osdQuery.On("Path", mock.Anything, mock.Anything, osd).Return(backing, nil).Once()
	m.osdQuery.On("Delete", mock.Anything, mock.Anything, osd).Return(nil).Once()
}

// TestDoRemoveOSDStopsProcessBeforeMarkingDown tests the order of the removal: the OSD is taken
// out, its process is stopped and seen gone, and only then is it marked down and purged.
func (s *osdSuite) TestDoRemoveOSDStopsProcessBeforeMarkingDown() {
	const osd = int64(7)
	m := s.setupRemoveOSD(osd)
	m.expectInCrushTree(osd)
	expectOSDProcessStopped(s.T(), m.runner, osd)
	m.runner.On("RunCommand", "ceph", "osd", "down", "osd.7").Return("", nil).Once()
	m.runner.On("RunCommand", "ceph", "osd", "purge", "osd.7", "--yes-i-really-mean-it").Return("", nil).Once()
	m.runner.On("RunCommand", "ceph", "osd", "ls", "-f", "json").Return("[0,1,2]", nil).Once()
	s.expectRecordRemoved(m, osd)

	err := doRemoveOSD(context.Background(), m.state, osd, true)
	require.NoError(s.T(), err)

	assert.Equal(s.T(), []string{
		"ceph config get mon osd_pool_default_crush_rule",
		"ceph osd crush rule dump microceph_auto_host",
		"ceph osd tree -f json",
		"ceph osd crush reweight osd.7 0.000000",
		"ceph osd out osd.7",
		"pkill -f ceph-osd .* --id 7$",
		"pgrep -f ceph-osd .* --id 7$",
		"ceph osd down osd.7",
		"ceph osd purge osd.7 --yes-i-really-mean-it",
		"ceph osd ls -f json",
	}, ranCommands(m.runner))
	assert.NoDirExists(s.T(), getOSDDataPath(osd))
}

// TestDoRemoveOSDDoesNotMarkDownWhenKillFails ensures a failed stop preserves local state and
// autostart without marking the OSD down or purging it.
func (s *osdSuite) TestDoRemoveOSDDoesNotMarkDownWhenKillFails() {
	const osd = int64(7)
	m := s.setupRemoveOSD(osd)
	m.expectInCrushTree(osd)
	expectOSDStopFails(s.T(), m.runner, osd, fmt.Errorf("stop failed"))

	err := doRemoveOSD(context.Background(), m.state, osd, true)
	assert.ErrorContains(s.T(), err, "failed to stop")
	assert.ErrorContains(s.T(), err, "stop failed")

	// Any further command, or a database change, would have failed the strict mocks as well.
	cmds := ranCommands(m.runner)
	assert.NotContains(s.T(), cmds, "ceph osd down osd.7")
	assert.NotContains(s.T(), cmds, "ceph osd purge osd.7 --yes-i-really-mean-it")
	osdDataPath := getOSDDataPath(osd)
	assert.FileExists(s.T(), osdReadyMarkerPath(osdDataPath))
	assert.NoFileExists(s.T(), osdSuppressedReadyMarkerPath(osdDataPath))
}

// TestDoRemoveOSDMarksDownAgainWhenBootIsCommittedLate tests the failure behind the "osd.N is not
// down" errors: the monitors commit the boot of the stopped OSD after the first down, so the
// purge is refused until the OSD is marked down again.
func (s *osdSuite) TestDoRemoveOSDMarksDownAgainWhenBootIsCommittedLate() {
	const osd = int64(7)
	m := s.setupRemoveOSD(osd)
	m.expectInCrushTree(osd)
	expectOSDProcessStopped(s.T(), m.runner, osd)
	m.runner.On("RunCommand", "ceph", "osd", "down", "osd.7").Return("", nil).Twice()
	m.runner.On("RunCommand", "ceph", "osd", "purge", "osd.7", "--yes-i-really-mean-it").Return("", notDownErr(osd)).Once()
	m.runner.On("RunCommand", "ceph", "osd", "purge", "osd.7", "--yes-i-really-mean-it").Return("", nil).Once()
	m.runner.On("RunCommand", "ceph", "osd", "ls", "-f", "json").Return("[0,1,2]", nil).Once()
	s.expectRecordRemoved(m, osd)

	err := doRemoveOSD(context.Background(), m.state, osd, true)
	require.NoError(s.T(), err)

	assert.Equal(s.T(), []string{
		"ceph osd out osd.7",
		"pkill -f ceph-osd .* --id 7$",
		"pgrep -f ceph-osd .* --id 7$",
		"ceph osd down osd.7",
		"ceph osd purge osd.7 --yes-i-really-mean-it",
		"ceph osd down osd.7",
		"ceph osd purge osd.7 --yes-i-really-mean-it",
		"ceph osd ls -f json",
	}, ranCommandsFrom(m.runner, "ceph osd out osd.7"))
}

// TestDoRemoveOSDNotYetInCephStopsProcessFirst tests that an OSD that is not in the CRUSH tree yet,
// as right after it was added, has its process stopped before it is taken out and down once it
// shows up.
func (s *osdSuite) TestDoRemoveOSDNotYetInCephStopsProcessFirst() {
	const osd = int64(7)
	m := s.setupRemoveOSD(osd)
	m.runner.On("RunCommand", "ceph", "osd", "tree", "-f", "json").Return(`{"nodes":[]}`, nil).Once()
	expectOSDProcessStopped(s.T(), m.runner, osd)
	m.expectInCrushTree(osd)
	m.runner.On("RunCommand", "ceph", "osd", "down", "osd.7").Return("", nil).Once()
	m.runner.On("RunCommand", "ceph", "osd", "purge", "osd.7", "--yes-i-really-mean-it").Return("", nil).Once()
	m.runner.On("RunCommand", "ceph", "osd", "ls", "-f", "json").Return("[0,1,2]", nil).Once()
	s.expectRecordRemoved(m, osd)

	err := doRemoveOSD(context.Background(), m.state, osd, true)
	require.NoError(s.T(), err)

	assert.Equal(s.T(), []string{
		"ceph osd tree -f json",
		"pkill -f ceph-osd .* --id 7$",
		"pgrep -f ceph-osd .* --id 7$",
		"ceph osd tree -f json",
		"ceph osd crush reweight osd.7 0.000000",
		"ceph osd out osd.7",
		"ceph osd down osd.7",
		"ceph osd purge osd.7 --yes-i-really-mean-it",
		"ceph osd ls -f json",
	}, ranCommandsFrom(m.runner, "ceph osd tree -f json"))
}

// noWaitForPresence makes doRemoveOSD look only once for an OSD that is not in the CRUSH tree,
// instead of waiting for it to show up.
func (s *osdSuite) noWaitForPresence() {
	origWindow := osdPresenceRetryWindow
	osdPresenceRetryWindow = -time.Second
	s.T().Cleanup(func() { osdPresenceRetryWindow = origWindow })
}

// TestDoRemoveOSDPurgesIdThatIsOnlyInOSDMap tests that an id that is in the OSD map but not in the
// CRUSH tree, as after a removal that failed because a purged id came back, is marked down and
// purged when the removal is run again, instead of being left in the OSD map after a success.
func (s *osdSuite) TestDoRemoveOSDPurgesIdThatIsOnlyInOSDMap() {
	const osd = int64(7)
	s.noWaitForPresence()
	m := s.setupRemoveOSD(osd)
	// ceph osd tree lists such an id as stray, not among the nodes of the CRUSH tree.
	m.runner.On("RunCommand", "ceph", "osd", "tree", "-f", "json").Return(`{"nodes":[],"stray":[{"id":7,"type":"osd"}]}`, nil).Twice()
	expectNoOSDProcess(s.T(), m.runner, osd)
	m.runner.On("RunCommand", "ceph", "osd", "ls", "-f", "json").Return("[0,1,2,7]", nil).Once()
	m.runner.On("RunCommand", "ceph", "osd", "down", "osd.7").Return("", nil).Once()
	m.runner.On("RunCommand", "ceph", "osd", "purge", "osd.7", "--yes-i-really-mean-it").Return("", nil).Once()
	m.runner.On("RunCommand", "ceph", "osd", "ls", "-f", "json").Return("[0,1,2]", nil).Once()
	s.expectRecordRemoved(m, osd)

	err := doRemoveOSD(context.Background(), m.state, osd, true)
	require.NoError(s.T(), err)

	assert.Equal(s.T(), []string{
		"ceph osd tree -f json",
		"pkill -f ceph-osd .* --id 7$",
		"ceph osd tree -f json",
		"ceph osd ls -f json",
		"ceph osd down osd.7",
		"ceph osd purge osd.7 --yes-i-really-mean-it",
		"ceph osd ls -f json",
	}, ranCommandsFrom(m.runner, "ceph osd tree -f json"))
	assert.NoDirExists(s.T(), getOSDDataPath(osd))
}

// TestDoRemoveOSDSkipsPurgeWhenIdIsNotInOSDMap tests that an OSD that is neither in the CRUSH tree
// nor in the OSD map, as when it never got that far, is removed locally without a down or a purge.
func (s *osdSuite) TestDoRemoveOSDSkipsPurgeWhenIdIsNotInOSDMap() {
	const osd = int64(7)
	s.noWaitForPresence()
	m := s.setupRemoveOSD(osd)
	m.runner.On("RunCommand", "ceph", "osd", "tree", "-f", "json").Return(`{"nodes":[]}`, nil).Twice()
	expectNoOSDProcess(s.T(), m.runner, osd)
	m.runner.On("RunCommand", "ceph", "osd", "ls", "-f", "json").Return("[0,1,2]", nil).Once()
	s.expectRecordRemoved(m, osd)

	err := doRemoveOSD(context.Background(), m.state, osd, true)
	require.NoError(s.T(), err)

	assert.NoDirExists(s.T(), getOSDDataPath(osd))
}

// TestDoRemoveOSDFailsWhenOSDMapCannotBeRead tests that an OSD that is not in the CRUSH tree is not
// removed locally while it is unknown whether its id is in the OSD map, and that its autostart
// marker comes back.
func (s *osdSuite) TestDoRemoveOSDFailsWhenOSDMapCannotBeRead() {
	const osd = int64(7)
	s.noWaitForPresence()
	m := s.setupRemoveOSD(osd)
	m.runner.On("RunCommand", "ceph", "osd", "tree", "-f", "json").Return(`{"nodes":[]}`, nil).Twice()
	expectNoOSDProcess(s.T(), m.runner, osd)
	m.runner.On("RunCommand", "ceph", "osd", "ls", "-f", "json").Return("", fmt.Errorf("monitors unreachable")).Once()

	err := doRemoveOSD(context.Background(), m.state, osd, true)
	assert.ErrorContains(s.T(), err, "failed to check if osd.7 is in the OSD map")

	osdDataPath := getOSDDataPath(osd)
	assert.DirExists(s.T(), osdDataPath)
	assert.FileExists(s.T(), osdReadyMarkerPath(osdDataPath))
	assert.NoFileExists(s.T(), osdSuppressedReadyMarkerPath(osdDataPath))
}

// TestTestSafeStop tests OSD safe stop check
func (s *osdSuite) TestTestSafeStop() {
	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	// Test safe to stop
	r.On("RunCommand", "ceph", "osd", "ok-to-stop", "osd.0").Return("", nil).Once()
	result := osdmgr.testSafeStop([]int64{0})
	assert.True(s.T(), result)

	// Test not safe to stop
	r.On("RunCommand", "ceph", "osd", "ok-to-stop", "osd.1").Return("", fmt.Errorf("not safe")).Once()
	result = osdmgr.testSafeStop([]int64{1})
	assert.False(s.T(), result)

	// Test multiple OSDs
	r.On("RunCommand", "ceph", "osd", "ok-to-stop", "osd.0", "osd.1").Return("", nil).Once()
	result = osdmgr.testSafeStop([]int64{0, 1})
	assert.True(s.T(), result)
}

// TestTestSafeDestroy tests OSD safe destroy check
func (s *osdSuite) TestTestSafeDestroy() {
	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	// Test safe to destroy
	r.On("RunCommand", "ceph", "osd", "safe-to-destroy", "osd.0").Return("", nil).Once()
	result := osdmgr.testSafeDestroy(0)
	assert.True(s.T(), result)

	// Test not safe to destroy
	r.On("RunCommand", "ceph", "osd", "safe-to-destroy", "osd.1").Return("", fmt.Errorf("not safe")).Once()
	result = osdmgr.testSafeDestroy(1)
	assert.False(s.T(), result)
}

// TestReweightOSD tests OSD reweighting
func (s *osdSuite) TestReweightOSD() {
	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	// Test successful reweight
	r.On("RunCommand", "ceph", "osd", "crush", "reweight", "osd.0", "0.000000").Return("", nil).Once()
	osdmgr.reweightOSD(context.Background(), 0, 0.0)

	// Test failed reweight (should only log warning, not return error)
	r.On("RunCommand", "ceph", "osd", "crush", "reweight", "osd.1", "1.000000").Return("", fmt.Errorf("reweight failed")).Once()
	osdmgr.reweightOSD(context.Background(), 1, 1.0)
}

// TestValidateAddOSDArgs tests OSD addition argument validation
func (s *osdSuite) TestValidateAddOSDArgs() {
	osdmgr := NewOSDManager(nil)

	// Test valid args
	data := types.DiskParameter{Path: "/dev/sdx"}
	err := osdmgr.validateAddOSDArgs(data, nil, nil)
	assert.NoError(s.T(), err)

	// Test valid loopback
	loopDataValid := types.DiskParameter{Path: "loop,4G,3"}
	err = osdmgr.validateAddOSDArgs(loopDataValid, nil, nil)
	assert.NoError(s.T(), err)

	// Test loopback with WAL, should fail as we req. a real block device
	loopData := types.DiskParameter{LoopSize: 1024}
	wal := &types.DiskParameter{Path: "/dev/wal"}
	err = osdmgr.validateAddOSDArgs(loopData, wal, nil)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "loopback and WAL/DB are mutually exclusive")

	// Test loopback with DB (should fail)
	db := &types.DiskParameter{Path: "/dev/db"}
	err = osdmgr.validateAddOSDArgs(loopData, nil, db)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "loopback and WAL/DB are mutually exclusive")

	// Test loopback with encryption (should fail)
	loopDataInvalid := types.DiskParameter{Path: "loop,4G,3", Encrypt: true}
	err = osdmgr.validateAddOSDArgs(loopDataInvalid, nil, nil)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "encryption is not supported on loop devices")
}

// TestIsPristineDisk tests pristine disk checking
func (s *osdSuite) TestIsPristineDisk() {
	// Create a custom OSD manager with mocked pristine checker
	osdmgr := &OSDManager{
		pristineChecker: &MockPristineChecker{},
	}

	mockPristineChecker := osdmgr.pristineChecker.(*MockPristineChecker)

	// Test pristine disk (all zeros)
	mockPristineChecker.On("IsPristineDisk", "/dev/pristine").Return(true, nil).Once()
	isPristine, err := osdmgr.pristineChecker.IsPristineDisk("/dev/pristine")
	assert.NoError(s.T(), err)
	assert.True(s.T(), isPristine)

	// Test non-pristine disk (has data)
	mockPristineChecker.On("IsPristineDisk", "/dev/used").Return(false, nil).Once()
	isPristine, err = osdmgr.pristineChecker.IsPristineDisk("/dev/used")
	assert.NoError(s.T(), err)
	assert.False(s.T(), isPristine)

	// Test error reading disk
	mockPristineChecker.On("IsPristineDisk", "/dev/error").Return(false, fmt.Errorf("permission denied")).Once()
	isPristine, err = osdmgr.pristineChecker.IsPristineDisk("/dev/error")
	assert.Error(s.T(), err)
	assert.False(s.T(), isPristine)
	assert.Contains(s.T(), err.Error(), "permission denied")
}

func (s *osdSuite) TestCheckPristineDeviceSkip() {
	osdmgr := &OSDManager{
		pristineChecker: &MockPristineChecker{},
	}

	disk := &types.DiskParameter{Path: "/dev/generated-aux1", SkipPristineCheck: true}
	err := osdmgr.checkPristineDevice(disk, "WAL")
	assert.NoError(s.T(), err)
	osdmgr.pristineChecker.(*MockPristineChecker).AssertNotCalled(s.T(), "IsPristineDisk", "/dev/generated-aux1")
}

// osdDumpJSON generates a JSON OSD dump with the given number of up OSDs.
func osdDumpJSON(upCount int) string {
	return osdDumpJSONMixed(upCount, 0)
}

// osdDumpJSONMixed generates a JSON OSD dump with a mix of up and down OSDs.
func osdDumpJSONMixed(upCount, downCount int) string {
	osds := "["
	for i := 0; i < upCount; i++ {
		if i > 0 {
			osds += ","
		}
		osds += fmt.Sprintf(`{"uuid":"osd-%d","up":1}`, i)
	}
	for i := 0; i < downCount; i++ {
		if upCount > 0 || i > 0 {
			osds += ","
		}
		osds += fmt.Sprintf(`{"uuid":"osd-down-%d","up":0}`, i)
	}
	osds += "]"
	return fmt.Sprintf(`{"osds":%s}`, osds)
}

func (s *osdSuite) TestWaitForOSDsReadyImmediateSuccess() {
	r := mocks.NewRunner(s.T())

	// 1 pool with size=1.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "ls", "--format", "json").
		Return(`["mypool"]`, nil).Once()
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "get", "mypool", "all", "--format", "json").
		Return(`{"pool":"mypool","pool_id":1,"size":1,"min_size":1,"crush_rule":""}`, nil).Once()
	// Found 1 OSD.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "dump", "-f", "json-pretty").
		Return(osdDumpJSON(1), nil).Once()

	common.ProcessExec = r

	// Should succeed: required 1 replica, found 1 OSD.
	err := WaitForOSDsReady(context.Background())
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

func (s *osdSuite) TestWaitForOSDsReadyRetriesThenSucceeds() {
	r := mocks.NewRunner(s.T())

	// Pool with size=3 (called each iteration).
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "ls", "--format", "json").
		Return(`["rbd"]`, nil)
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "get", "rbd", "all", "--format", "json").
		Return(`{"pool":"rbd","pool_id":1,"size":3,"min_size":2,"crush_rule":""}`, nil)
	// First poll: only 2 OSDs up.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "dump", "-f", "json-pretty").
		Return(osdDumpJSON(2), nil).Once()
	// Second poll: 3 OSDs up.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "dump", "-f", "json-pretty").
		Return(osdDumpJSON(3), nil).Once()

	common.ProcessExec = r

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := WaitForOSDsReady(ctx)
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

func (s *osdSuite) TestWaitForOSDsReadyWithDownOSDs() {
	r := mocks.NewRunner(s.T())

	// Pool with size=3 (called each iteration).
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "ls", "--format", "json").
		Return(`["rbd"]`, nil)
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "get", "rbd", "all", "--format", "json").
		Return(`{"pool":"rbd","pool_id":1,"size":3,"min_size":2,"crush_rule":""}`, nil)
	// First poll: 2 up, 2 down — not enough.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "dump", "-f", "json-pretty").
		Return(osdDumpJSONMixed(2, 2), nil).Once()
	// Second poll: 3 up, 1 down — sufficient.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "dump", "-f", "json-pretty").
		Return(osdDumpJSONMixed(3, 1), nil).Once()

	common.ProcessExec = r

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := WaitForOSDsReady(ctx)
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

func (s *osdSuite) TestWaitForOSDsReadyFallbackToDefault() {
	r := mocks.NewRunner(s.T())

	// No pools.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "ls", "--format", "json").
		Return(`[]`, nil).Once()
	// Default size = 3.
	r.On("RunCommandContext", mock.Anything, "ceph", "config", "get", "mon", "osd_pool_default_size").
		Return("3\n", nil).Once()
	// 3 OSDs up.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "dump", "-f", "json-pretty").
		Return(osdDumpJSON(3), nil).Once()

	common.ProcessExec = r

	err := WaitForOSDsReady(context.Background())
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

func (s *osdSuite) TestWaitForOSDsReadyTimeout() {
	r := mocks.NewRunner(s.T())

	// 1 pool with size=1 (called each iteration).
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "ls", "--format", "json").
		Return(`["mypool"]`, nil)
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "pool", "get", "mypool", "all", "--format", "json").
		Return(`{"pool":"mypool","pool_id":1,"size":1,"min_size":1,"crush_rule":""}`, nil)
	// OSD dump always fails.
	r.On("RunCommandContext", mock.Anything, "ceph", "osd", "dump", "-f", "json-pretty").
		Return("", errors.New("connection refused"))

	common.ProcessExec = r

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := WaitForOSDsReady(ctx)
	assert.Error(s.T(), err)
	assert.ErrorIs(s.T(), err, context.DeadlineExceeded)
}

// rackDegradeTestSetup configures mocks for IsRackDegradeBlocked tests.
// It sets up ProcessExec (ceph commands), OSDQuery (disk list), and getAZData.
// Returns a restore function.
func rackDegradeTestSetup(t *testing.T, opts rackDegradeOpts) func() {
	origProcessExec := common.ProcessExec
	origOSDQuery := database.OSDQuery
	origGetAZData := getAZData

	r := mocks.NewRunner(t)

	// getDefaultCrushRule -> ceph config get mon osd_pool_default_crush_rule
	r.On("RunCommand", "ceph", "config", "get", "mon", "osd_pool_default_crush_rule").
		Return(opts.defaultRuleID+"\n", nil).Maybe()

	// getCrushRuleID("microceph_auto_rack")
	if opts.rackRuleErr != nil {
		r.On("RunCommand", "ceph", "osd", "crush", "rule", "dump", "microceph_auto_rack").
			Return("", opts.rackRuleErr).Maybe()
	} else {
		r.On("RunCommand", "ceph", "osd", "crush", "rule", "dump", "microceph_auto_rack").
			Return(fmt.Sprintf(`{"rule_id": %s}`, opts.rackRuleID), nil).Maybe()
	}

	// getOSDTreeNodes -> ceph osd tree (via cephRunContext -> RunCommandContext)
	if opts.osdTree != "" {
		r.On("RunCommandContext", mock.Anything, "ceph", "osd", "tree", "-f", "json").
			Return(opts.osdTree, nil).Maybe()
	}

	common.ProcessExec = r

	// Mock OSDQuery.List
	m := mocks.NewOSDQueryInterface(t)
	m.On("List", mock.Anything, mock.Anything).Return(opts.disks, nil).Maybe()
	database.OSDQuery = m

	// Mock getAZData
	getAZData = func(_ context.Context, _ mcTypes.State, hostname string) (azData, error) {
		if d, ok := opts.azDataByHost[hostname]; ok {
			return d, nil
		}
		return opts.azDataDefault, nil
	}

	return func() {
		common.ProcessExec = origProcessExec
		database.OSDQuery = origOSDQuery
		getAZData = origGetAZData
	}
}

type rackDegradeOpts struct {
	defaultRuleID string
	rackRuleID    string
	rackRuleErr   error
	osdTree       string
	disks         types.Disks
	azDataByHost  map[string]azData // keyed by hostname
	azDataDefault azData            // fallback for unmatched hosts
}

func (s *osdSuite) TestIsRackDegradeBlockedNotOnRack() {
	defer rackDegradeTestSetup(s.T(), rackDegradeOpts{
		defaultRuleID: "2", // host rule
		rackRuleID:    "3", // rack rule — different, so not on rack
	})()

	si := mocks.NewStateInterface(s.T())

	blocked, err := IsRackDegradeBlocked(context.Background(), si, 0)
	assert.NoError(s.T(), err)
	assert.False(s.T(), blocked)
}

func (s *osdSuite) TestIsRackDegradeBlockedLastOSDInAZ() {
	// On rack rule, osd.2 is the only OSD in az-c — should block
	defer rackDegradeTestSetup(s.T(), rackDegradeOpts{
		defaultRuleID: "3",
		rackRuleID:    "3",
		osdTree:       osdTreeWithOSDs([]string{"az-a", "az-b", "az-c"}),
		disks: types.Disks{
			{OSD: 0, Location: "host-az-a"},
			{OSD: 1, Location: "host-az-b"},
			{OSD: 2, Location: "host-az-c"},
		},
		azDataByHost: map[string]azData{
			"host-az-c": {hostAZ: "az-c", uniqueAZs: map[string]bool{"az-a": true, "az-b": true, "az-c": true}},
		},
		azDataDefault: azData{
			hostAZ:    "az-a",
			uniqueAZs: map[string]bool{"az-a": true, "az-b": true, "az-c": true},
		},
	})()

	u := api.NewURL()
	st := &mocks.MockState{URL: u, ClusterName: "host-az-a"}
	si := mocks.NewStateInterface(s.T())
	si.On("ClusterState").Return(st).Maybe()

	blocked, err := IsRackDegradeBlocked(context.Background(), si, 2)
	assert.NoError(s.T(), err)
	assert.True(s.T(), blocked)
}

func (s *osdSuite) TestIsRackDegradeBlockedNotLastOSD() {
	// On rack rule, az-c has 2 OSDs — removing one still leaves az-c active
	treeWith2InAZC := `{"nodes":[
		{"id":-1,"name":"default","type":"root","children":[-2,-3,-4]},
		{"id":-2,"name":"az.az-a","type":"rack","children":[-5]},
		{"id":-5,"name":"host-az-a","type":"host","children":[0]},
		{"id":0,"name":"osd.0","type":"osd"},
		{"id":-3,"name":"az.az-b","type":"rack","children":[-6]},
		{"id":-6,"name":"host-az-b","type":"host","children":[1]},
		{"id":1,"name":"osd.1","type":"osd"},
		{"id":-4,"name":"az.az-c","type":"rack","children":[-7]},
		{"id":-7,"name":"host-az-c","type":"host","children":[2,3]},
		{"id":2,"name":"osd.2","type":"osd"},
		{"id":3,"name":"osd.3","type":"osd"}
	]}`

	defer rackDegradeTestSetup(s.T(), rackDegradeOpts{
		defaultRuleID: "3",
		rackRuleID:    "3",
		osdTree:       treeWith2InAZC,
		disks: types.Disks{
			{OSD: 0, Location: "host-az-a"},
			{OSD: 1, Location: "host-az-b"},
			{OSD: 2, Location: "host-az-c"},
			{OSD: 3, Location: "host-az-c"},
		},
		azDataByHost: map[string]azData{
			"host-az-c": {hostAZ: "az-c", uniqueAZs: map[string]bool{"az-a": true, "az-b": true, "az-c": true}},
		},
		azDataDefault: azData{
			hostAZ:    "az-a",
			uniqueAZs: map[string]bool{"az-a": true, "az-b": true, "az-c": true},
		},
	})()

	u := api.NewURL()
	st := &mocks.MockState{URL: u, ClusterName: "host-az-a"}
	si := mocks.NewStateInterface(s.T())
	si.On("ClusterState").Return(st).Maybe()

	blocked, err := IsRackDegradeBlocked(context.Background(), si, 2)
	assert.NoError(s.T(), err)
	assert.False(s.T(), blocked)
}

func (s *osdSuite) TestIsRackDegradeBlockedMoreThan3AZs() {
	// On rack rule with 4 AZs — losing one still leaves 3
	defer rackDegradeTestSetup(s.T(), rackDegradeOpts{
		defaultRuleID: "3",
		rackRuleID:    "3",
		osdTree:       osdTreeWithOSDs([]string{"az-a", "az-b", "az-c", "az-d"}),
		disks: types.Disks{
			{OSD: 0, Location: "host-az-a"},
			{OSD: 1, Location: "host-az-b"},
			{OSD: 2, Location: "host-az-c"},
			{OSD: 3, Location: "host-az-d"},
		},
		azDataByHost: map[string]azData{
			"host-az-d": {hostAZ: "az-d", uniqueAZs: map[string]bool{"az-a": true, "az-b": true, "az-c": true, "az-d": true}},
		},
		azDataDefault: azData{
			hostAZ:    "az-a",
			uniqueAZs: map[string]bool{"az-a": true, "az-b": true, "az-c": true, "az-d": true},
		},
	})()

	u := api.NewURL()
	st := &mocks.MockState{URL: u, ClusterName: "host-az-a"}
	si := mocks.NewStateInterface(s.T())
	si.On("ClusterState").Return(st).Maybe()

	blocked, err := IsRackDegradeBlocked(context.Background(), si, 3)
	assert.NoError(s.T(), err)
	assert.False(s.T(), blocked)
}

// TestGetStorageWithRetry is a regression test for the udevd TOCTOU race in
// /dev/disk/by-id/. udevd atomically replaces symlinks by writing a .#-prefixed
// temp entry then renaming it. LXD's GetStorage() can see the .# entry in readdir
// and then get ENOENT on lstat because the rename completed between the two calls.
// getStorageWithRetry must absorb that transient error and succeed on the next attempt.
func (s *osdSuite) TestGetStorageWithRetry() {
	storageRetrySleepFunc = func(_ time.Duration) {}
	s.T().Cleanup(func() { storageRetrySleepFunc = time.Sleep })

	osdmgr := NewOSDManager(nil)
	goodStorage := &api.ResourcesStorage{
		Disks: []api.ResourcesStorageDisk{{Device: "sda"}},
	}

	calls := 0
	mockStorage := mocks.NewStorageInterface(s.T())
	mockStorage.On("GetStorage").Return(func() (*api.ResourcesStorage, error) {
		calls++
		if calls == 1 {
			// Exact error from the CI failure: udevd renamed .#scsi-... out from
			// under LXD's EvalSymlinks call.
			return nil, fmt.Errorf(`Failed to find "/dev/disk/by-id/.#scsi-0QEMU_QEMU_HARDDISK_lxd_microceph--dsl--01d14fb4b757b3ed": lstat /dev/disk/by-id/.#scsi-0QEMU_QEMU_HARDDISK_lxd_microceph--dsl--01d14fb4b757b3ed: no such file or directory`)
		}
		return goodStorage, nil
	})
	osdmgr.storage = mockStorage

	storage, err := osdmgr.getStorageWithRetry()

	require.NoError(s.T(), err)
	assert.Equal(s.T(), goodStorage, storage)
	assert.Equal(s.T(), 2, calls)
}

// TestGetStorageWithRetryExhausted verifies that errors are propagated after all
// retry attempts are exhausted, and that GetStorage() is called exactly maxAttempts
// times before giving up.
func (s *osdSuite) TestGetStorageWithRetryExhausted() {
	storageRetrySleepFunc = func(_ time.Duration) {}
	s.T().Cleanup(func() { storageRetrySleepFunc = time.Sleep })

	osdmgr := NewOSDManager(nil)
	persistentErr := fmt.Errorf("persistent storage error")

	mockStorage := mocks.NewStorageInterface(s.T())
	mockStorage.On("GetStorage").Return(nil, persistentErr).Times(3)
	osdmgr.storage = mockStorage

	_, err := osdmgr.getStorageWithRetry()

	require.ErrorIs(s.T(), err, persistentErr)
}

// TestCheckStorageEligibility verifies that CheckStorageEligibility enforces OSD enrollment restrictions based on the declarative placement policy.
func (s *osdSuite) TestCheckStorageEligibility() {
	ctx := context.Background()

	// Case 1: State is nil. Any member is allowed.
	{
		osdmgr := NewOSDManager(nil)
		err := osdmgr.checkStorageEligibility(ctx)
		assert.NoError(s.T(), err)
	}

	// Helper to create a mock state with in-memory SQLite and populate the placement policy table.
	createTestState := func(active bool, policyJSON string) (*mocks.MockState, func()) {
		state, _, cleanup := newPlacementPolicyTestState(s.T(), active, policyJSON)
		return state, cleanup
	}

	// Case 2. Active is false, thus there's no enforcement. Allow.
	{
		state, cleanup := createTestState(false, "")
		defer cleanup()

		osdmgr := NewOSDManager(state)
		err := osdmgr.checkStorageEligibility(ctx)
		assert.NoError(s.T(), err)
	}

	// Case 3: Active is true and policy is empty. Reject.
	{
		state, cleanup := createTestState(true, "")
		defer cleanup()

		osdmgr := NewOSDManager(state)
		err := osdmgr.checkStorageEligibility(ctx)
		assert.Error(s.T(), err)
	}

	// Case 4: Active is true and member has storage eligibility. Allow.
	{
		policyJSON := `{"members":{"node-a":{"storage_eligible":true}}}`
		state, cleanup := createTestState(true, policyJSON)
		defer cleanup()

		osdmgr := NewOSDManager(state)
		err := osdmgr.checkStorageEligibility(ctx)
		assert.NoError(s.T(), err)
	}

	// Case 5: Active is true but member has no storage eligibility. Reject.
	{
		policyJSON := `{"members":{"node-a":{"storage_eligible":false}}}`
		state, cleanup := createTestState(true, policyJSON)
		defer cleanup()

		osdmgr := NewOSDManager(state)
		err := osdmgr.checkStorageEligibility(ctx)
		assert.Error(s.T(), err)
	}

	// Case 6: Active is true but member is omitted. Reject.
	{
		policyJSON := `{"members":{"node-b":{"storage_eligible":true}}}`
		state, cleanup := createTestState(true, policyJSON)
		defer cleanup()

		osdmgr := NewOSDManager(state)
		err := osdmgr.checkStorageEligibility(ctx)
		assert.Error(s.T(), err)
	}

	// Case 7: Active is true, member is present, but storage_eligible is
	// omitted. Reject: storage is the fail-closed dimension, so an unmanaged
	// eligibility is not a grant. Because PUT replaces the whole policy, this
	// is also how a snapshot that drops a previous grant revokes it -- the
	// omitted field is never inherited from the superseded policy.
	{
		policyJSON := `{"members":{"node-a":{"control":true}}}`
		state, cleanup := createTestState(true, policyJSON)
		defer cleanup()

		osdmgr := NewOSDManager(state)
		err := osdmgr.checkStorageEligibility(ctx)
		assert.Error(s.T(), err)
	}

	// Case 8: Active is true with the CE142 waiting policy (an empty members
	// map). Reject: an empty map is an empty storage allow-list, not an absent
	// policy, so no member may enroll new OSDs.
	{
		policyJSON := `{"mode":"reconcile","members":{}}`
		state, cleanup := createTestState(true, policyJSON)
		defer cleanup()

		osdmgr := NewOSDManager(state)
		err := osdmgr.checkStorageEligibility(ctx)
		assert.Error(s.T(), err)
	}
}

// TestCheckStorageEligibilityPolicyReplacementRevokesGrant verifies that
// storing a second policy replaces the first rather than merging with it: a
// grant made by the first snapshot does not survive into a second snapshot that
// omits it. This is the enforcement-point expression of the PUT-is-replacement
// contract documented on types.PlacementPolicy.
func (s *osdSuite) TestCheckStorageEligibilityPolicyReplacementRevokesGrant() {
	ctx := context.Background()

	state, db, cleanup := newPlacementPolicyTestState(s.T(), false, "")
	defer cleanup()

	osdmgr := NewOSDManager(state)

	storePolicy := func(policyJSON string) {
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(s.T(), err)
		err = database.SetPlacementPolicy(ctx, tx, true, policyJSON)
		require.NoError(s.T(), err)
		require.NoError(s.T(), tx.Commit())
	}

	// First snapshot grants storage on the local member.
	storePolicy(`{"mode":"reconcile","members":{"node-a":{"storage_eligible":true}}}`)
	assert.NoError(s.T(), osdmgr.checkStorageEligibility(ctx))

	// Second snapshot keeps node-a but declares only control. The omitted
	// storage_eligible is unmanaged under the new policy, not inherited, so the
	// grant is gone.
	storePolicy(`{"mode":"reconcile","members":{"node-a":{"control":true}}}`)
	assert.Error(s.T(), osdmgr.checkStorageEligibility(ctx))

	// Third snapshot drops node-a entirely. An absent member is likewise not on
	// the allow-list.
	storePolicy(`{"mode":"reconcile","members":{"node-b":{"storage_eligible":true}}}`)
	assert.Error(s.T(), osdmgr.checkStorageEligibility(ctx))

	// A fresh grant re-enables enrollment, proving the denials above came from
	// the replacement policy rather than sticky state.
	storePolicy(`{"mode":"reconcile","members":{"node-a":{"storage_eligible":true}}}`)
	assert.NoError(s.T(), osdmgr.checkStorageEligibility(ctx))
}

// newPlacementPolicyTestState builds a MockState backed by an in-memory SQLite
// database carrying the placement_policy singleton row, seeded with the given
// active flag and policy JSON. It returns the state, the underlying handle so
// callers can rewrite the row mid-test, and a cleanup function.
func newPlacementPolicyTestState(t require.TestingT, active bool, policyJSON string) (*mocks.MockState, *sql.DB, func()) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)

	_, err = db.Exec(`
CREATE TABLE placement_policy (
  id               INTEGER PRIMARY KEY NOT NULL CHECK (id = 1),
  active           INTEGER NOT NULL DEFAULT 0,
  policy_json      TEXT,
  last_refusal     TEXT,
  apply_lock_token INTEGER NOT NULL DEFAULT 0
);
INSERT INTO placement_policy (id, active, policy_json) VALUES (1, 0, '');
`)
	require.NoError(t, err)

	activeInt := 0
	if active {
		activeInt = 1
	}
	_, err = db.Exec("UPDATE placement_policy SET active = ?, policy_json = ? WHERE id = 1", activeInt, policyJSON)
	require.NoError(t, err)

	txFn := func(ctx context.Context, f func(context.Context, *sql.Tx) error) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		err = f(ctx, tx)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	}

	state := &mocks.MockState{
		ClusterName: "node-a",
		DBObj:       &mocks.MockDB{TxFn: txFn},
	}

	return state, db, func() { db.Close() }
}
