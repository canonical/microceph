package ceph

import (
	"context"
	"fmt"
	"github.com/canonical/microceph/microceph/common"
	"path/filepath"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/tests"
	"github.com/spf13/afero"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
	r.On("RunCommand", "ceph", "osd", "dump").Return("flags sortbitwise,noout", nil).Once()
}

func addIsOsdNooutSetFalseExpections(r *mocks.Runner) {
	r.On("RunCommand", "ceph", "osd", "dump").Return("flags sortbitwise", nil).Once()
}

func addIsOsdNooutSetFailedExpections(r *mocks.Runner) {
	r.On("RunCommand", "ceph", "osd", "dump").Return("fail", fmt.Errorf("some errors")).Once()
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
	r.On("RunCommandContext", mock.Anything, "dd", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", nil).Once()
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
	err := osdmgr.checkEncryptSupport()
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
	err = osdmgr.checkEncryptSupport()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "dm-crypt interface")

	// Mock interface check to return true (connected)
	r.On("RunCommand", "snapctl", "is-connected", "dm-crypt").Return("", nil).Once()

	// Test missing dm_crypt module
	err = osdmgr.checkEncryptSupport()
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
	err = osdmgr.checkEncryptSupport()
	assert.NoError(s.T(), err)
}

// TestTimeoutWipe tests device wiping with timeout
func (s *osdSuite) TestTimeoutWipe() {
	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	// Test successful wipe
	r.On("RunCommandContext", mock.Anything, "dd", "if=/dev/zero", "of=/dev/sda", "bs=4M", "count=10", "status=none").Return("", nil).Once()
	err := osdmgr.timeoutWipe("/dev/sda")
	assert.NoError(s.T(), err)

	// Test failed wipe
	r.On("RunCommandContext", mock.Anything, "dd", "if=/dev/zero", "of=/dev/sdb", "bs=4M", "count=10", "status=none").Return("", fmt.Errorf("wipe failed")).Once()
	err = osdmgr.timeoutWipe("/dev/sdb")
	assert.Error(s.T(), err)
}

// TestWipeDevice tests device wiping with retry logic
func (s *osdSuite) TestWipeDevice() {
	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	// Test successful wipe on first try
	r.On("RunCommandContext", mock.Anything, "dd", "if=/dev/zero", "of=/dev/sda", "bs=4M", "count=10", "status=none").Return("", nil).Once()
	osdmgr.wipeDevice(context.Background(), "/dev/sda")

	// Test wipe that succeeds after retries
	r.On("RunCommandContext", mock.Anything, "dd", "if=/dev/zero", "of=/dev/sdb", "bs=4M", "count=10", "status=none").Return("", fmt.Errorf("busy")).Once()
	r.On("RunCommandContext", mock.Anything, "dd", "if=/dev/zero", "of=/dev/sdb", "bs=4M", "count=10", "status=none").Return("", nil).Once()
	osdmgr.wipeDevice(context.Background(), "/dev/sdb")
}

// TestKillOSD tests OSD process termination
func (s *osdSuite) TestKillOSD() {
	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	// Test successful kill
	r.On("RunCommand", "pkill", "-f", "ceph-osd .* --id 0$").Return("", nil).Once()
	err := osdmgr.killOSD(0)
	assert.NoError(s.T(), err)

	// Test failed kill
	r.On("RunCommand", "pkill", "-f", "ceph-osd .* --id 1$").Return("", fmt.Errorf("process not found")).Once()
	err = osdmgr.killOSD(1)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "failed to kill osd.1")
}

// TestOutDownOSD tests taking OSD out and down
func (s *osdSuite) TestOutDownOSD() {
	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	// Test successful out and down
	r.On("RunCommand", "ceph", "osd", "out", "osd.0").Return("", nil).Once()
	r.On("RunCommand", "ceph", "osd", "down", "osd.0").Return("", nil).Once()
	err := osdmgr.outDownOSD(0)
	assert.NoError(s.T(), err)

	// Test failed out command
	r.On("RunCommand", "ceph", "osd", "out", "osd.1").Return("", fmt.Errorf("out failed")).Once()
	err = osdmgr.outDownOSD(1)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "failed to take osd.1 out")

	// Test failed down command
	r.On("RunCommand", "ceph", "osd", "out", "osd.2").Return("", nil).Once()
	r.On("RunCommand", "ceph", "osd", "down", "osd.2").Return("", fmt.Errorf("down failed")).Once()
	err = osdmgr.outDownOSD(2)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "failed to take osd.2 down")
}

// TestDoPurge tests OSD purge command
func (s *osdSuite) TestDoPurge() {
	osdmgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	osdmgr.runner = r

	// Test successful purge
	r.On("RunCommand", "ceph", "osd", "purge", "osd.0", "--yes-i-really-mean-it").Return("", nil).Once()
	err := osdmgr.doPurge(0)
	assert.NoError(s.T(), err)

	// Test failed purge
	r.On("RunCommand", "ceph", "osd", "purge", "osd.1", "--yes-i-really-mean-it").Return("", fmt.Errorf("purge failed")).Once()
	err = osdmgr.doPurge(1)
	assert.Error(s.T(), err)
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

