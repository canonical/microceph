package ceph

import (
	"context"
	"fmt"
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

// osdSuite is the test suite for adding OSDs.
type osdSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
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

	processExec = r

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

	processExec = r

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

	processExec = r

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

	// patch processExec
	processExec = r

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

	// patch processExec
	processExec = r

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

	// patch processExec
	processExec = r

	err := setOsdNooutFlag(true)
	assert.NoError(s.T(), err)

	err = setOsdNooutFlag(false)
	assert.NoError(s.T(), err)
}

// TestSetOsdNooutFlagFail tests the setOsdNooutFlag function when error occurs
func (s *osdSuite) TestSetOsdNooutFlagFail() {
	r := mocks.NewRunner(s.T())
	addOsdtNooutFlagFailedExpectations(r)

	// patch processExec
	processExec = r

	err := setOsdNooutFlag(true)
	assert.Error(s.T(), err)
}

// TestIsOsdNooutSetOkay tests the isOsdNooutSet function when no error occurs
func (s *osdSuite) TestIsOsdNooutSetOkay() {
	r := mocks.NewRunner(s.T())
	addIsOsdNooutSetTrueExpections(r)
	addIsOsdNooutSetFalseExpections(r)

	// patch processExec
	processExec = r

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

	// patch processExec
	processExec = r

	// error running ceph osd dump
	set, err := isOsdNooutSet()
	assert.False(s.T(), set)
	assert.Error(s.T(), err)
}

// TestAddBulkDisksValidation ensures batch addition arguments are checked.
func (s *osdSuite) TestAddBulkDisksValidation() {
	mgr := NewOSDManager(nil)
	mgr.fs = afero.NewMemMapFs()
	disks := []types.DiskParameter{
		{Path: "/dev/sda"},
		{Path: "/dev/sdb"},
	}
	wal := &types.DiskParameter{Path: "/dev/wal"}

	resp := mgr.addBulkDisks(context.Background(), disks, wal, nil)
	assert.NotEmpty(s.T(), resp.ValidationError)
	assert.Equal(s.T(), "Failure", resp.Reports[0].Report)
}

// TestNewOSDManager tests the OSDManager constructor
func (s *osdSuite) TestNewOSDManager() {
	state := &mocks.MockState{}
	mgr := NewOSDManager(state)
	assert.NotNil(s.T(), mgr)
	assert.Equal(s.T(), state, mgr.state)
	assert.NotNil(s.T(), mgr.runner)
	assert.NotNil(s.T(), mgr.fs)
}

// TestSetStablePath tests device path stabilization
func (s *osdSuite) TestSetStablePath() {
	mgr := NewOSDManager(nil)
	fs := afero.NewMemMapFs()
	mgr.fs = fs

	// Create mock validator
	mockValidator := &MockPathValidator{}
	mgr.validator = mockValidator

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
	err := mgr.setStablePath(storage, param)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "invalid disk path")

	// Test with valid device path
	param2 := &types.DiskParameter{Path: "/dev/sda"}
	mockValidator.On("IsBlockdevPath", "/dev/sda").Return(true).Once()
	err = mgr.setStablePath(storage, param2)
	assert.NoError(s.T(), err)
}

// TestStabilizeDevicePathSuccess tests successful device path stabilization
func (s *osdSuite) TestStabilizeDevicePathSuccess() {
	mgr := NewOSDManager(nil)
	mgr.fs = afero.NewMemMapFs()

	// Mock storage interface
	mockStorage := mocks.NewStorageInterface(s.T())
	mgr.storage = mockStorage

	// Mock validator
	mockValidator := &MockPathValidator{}
	mgr.validator = mockValidator

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
	mockValidator.On("IsBlockdevPath", "/dev/sda").Return(true).Once()

	physParam := &types.DiskParameter{Path: "/dev/sda"}
	storage, err := mgr.stabilizeDevicePath(physParam)

	// Should succeed now with mocked validation
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), expectedStorage, storage)
}

// TestPrepareDisk tests disk preparation
func (s *osdSuite) TestPrepareDisk() {
	mgr := NewOSDManager(nil)
	// Use OsFs for symlink support, but in a temp directory
	mgr.fs = afero.NewOsFs()

	// Mock runner for wipe operations
	r := mocks.NewRunner(s.T())
	mgr.runner = r

	// Create temp directory for test
	tempDir := s.T().TempDir()
	osdPath := filepath.Join(tempDir, "ceph-0")
	err := mgr.fs.MkdirAll(osdPath, 0755)
	assert.NoError(s.T(), err)

	// Test without wipe or encrypt
	disk := &types.DiskParameter{Path: "/dev/sda"}
	err = mgr.prepareDisk(disk, "", osdPath, 0)
	assert.NoError(s.T(), err)

	// Verify symlink was created for data device (suffix == "")
	linkPath := filepath.Join(osdPath, "block")
	exists, err := afero.Exists(mgr.fs, linkPath)
	assert.NoError(s.T(), err)
	assert.True(s.T(), exists)

	// Test WAL device (suffix != "", no symlink should be created)
	walOsdPath := filepath.Join(tempDir, "ceph-1")
	err = mgr.fs.MkdirAll(walOsdPath, 0755)
	assert.NoError(s.T(), err)
	walDisk := &types.DiskParameter{Path: "/dev/sdb"}
	err = mgr.prepareDisk(walDisk, ".wal", walOsdPath, 1)
	assert.NoError(s.T(), err)

	// Test with wipe - use a different temp directory to avoid symlink conflicts
	wipeOsdPath := filepath.Join(tempDir, "ceph-2")
	err = mgr.fs.MkdirAll(wipeOsdPath, 0755)
	assert.NoError(s.T(), err)
	r.On("RunCommandContext", mock.Anything, "dd", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", nil).Once()
	wipeDisk := &types.DiskParameter{Path: "/dev/sdc", Wipe: true}
	err = mgr.prepareDisk(wipeDisk, "", wipeOsdPath, 2)
	assert.NoError(s.T(), err)
}

// TestCheckEncryptSupport tests encryption support validation
func (s *osdSuite) TestCheckEncryptSupport() {
	mgr := NewOSDManager(nil)
	fs := afero.NewMemMapFs()
	mgr.fs = fs

	// Mock the processExec for isIntfConnected calls
	r := mocks.NewRunner(s.T())
	originalProcessExec := processExec
	processExec = r
	defer func() { processExec = originalProcessExec }()

	// Test missing /dev/mapper/control
	err := mgr.checkEncryptSupport()
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
	err = mgr.checkEncryptSupport()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "dm-crypt interface")

	// Mock interface check to return true (connected)
	r.On("RunCommand", "snapctl", "is-connected", "dm-crypt").Return("", nil).Once()

	// Test missing dm_crypt module
	err = mgr.checkEncryptSupport()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "missing dm_crypt module")

	// Create dm_crypt module
	err = fs.MkdirAll("/sys/module/dm_crypt", 0755)
	assert.NoError(s.T(), err)

	// Mock interface check again for final test
	r.On("RunCommand", "snapctl", "is-connected", "dm-crypt").Return("", nil).Once()

	// Test /run access
	err = fs.MkdirAll("/run", 0755)
	assert.NoError(s.T(), err)

	err = mgr.checkEncryptSupport()
	// Should pass all checks now
	assert.NoError(s.T(), err)
}

// TestTimeoutWipe tests device wiping with timeout
func (s *osdSuite) TestTimeoutWipe() {
	mgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	mgr.runner = r

	// Test successful wipe
	r.On("RunCommandContext", mock.Anything, "dd", "if=/dev/zero", "of=/dev/sda", "bs=4M", "count=10", "status=none").Return("", nil).Once()
	err := mgr.timeoutWipe("/dev/sda")
	assert.NoError(s.T(), err)

	// Test failed wipe
	r.On("RunCommandContext", mock.Anything, "dd", "if=/dev/zero", "of=/dev/sdb", "bs=4M", "count=10", "status=none").Return("", fmt.Errorf("wipe failed")).Once()
	err = mgr.timeoutWipe("/dev/sdb")
	assert.Error(s.T(), err)
}

// TestWipeDevice tests device wiping with retry logic
func (s *osdSuite) TestWipeDevice() {
	mgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	mgr.runner = r

	// Test successful wipe on first try
	r.On("RunCommandContext", mock.Anything, "dd", "if=/dev/zero", "of=/dev/sda", "bs=4M", "count=10", "status=none").Return("", nil).Once()
	mgr.wipeDevice(context.Background(), "/dev/sda")

	// Test wipe that succeeds after retries
	r.On("RunCommandContext", mock.Anything, "dd", "if=/dev/zero", "of=/dev/sdb", "bs=4M", "count=10", "status=none").Return("", fmt.Errorf("busy")).Once()
	r.On("RunCommandContext", mock.Anything, "dd", "if=/dev/zero", "of=/dev/sdb", "bs=4M", "count=10", "status=none").Return("", nil).Once()
	mgr.wipeDevice(context.Background(), "/dev/sdb")
}

// TestKillOSD tests OSD process termination
func (s *osdSuite) TestKillOSD() {
	mgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	mgr.runner = r

	// Test successful kill
	r.On("RunCommand", "pkill", "-f", "ceph-osd .* --id 0$").Return("", nil).Once()
	err := mgr.killOSD(0)
	assert.NoError(s.T(), err)

	// Test failed kill
	r.On("RunCommand", "pkill", "-f", "ceph-osd .* --id 1$").Return("", fmt.Errorf("process not found")).Once()
	err = mgr.killOSD(1)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "failed to kill osd.1")
}

// TestOutDownOSD tests taking OSD out and down
func (s *osdSuite) TestOutDownOSD() {
	mgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	mgr.runner = r

	// Test successful out and down
	r.On("RunCommand", "ceph", "osd", "out", "osd.0").Return("", nil).Once()
	r.On("RunCommand", "ceph", "osd", "down", "osd.0").Return("", nil).Once()
	err := mgr.outDownOSD(0)
	assert.NoError(s.T(), err)

	// Test failed out command
	r.On("RunCommand", "ceph", "osd", "out", "osd.1").Return("", fmt.Errorf("out failed")).Once()
	err = mgr.outDownOSD(1)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "failed to take osd.1 out")

	// Test failed down command
	r.On("RunCommand", "ceph", "osd", "out", "osd.2").Return("", nil).Once()
	r.On("RunCommand", "ceph", "osd", "down", "osd.2").Return("", fmt.Errorf("down failed")).Once()
	err = mgr.outDownOSD(2)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "failed to take osd.2 down")
}

// TestDoPurge tests OSD purge command
func (s *osdSuite) TestDoPurge() {
	mgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	mgr.runner = r

	// Test successful purge
	r.On("RunCommand", "ceph", "osd", "purge", "osd.0", "--yes-i-really-mean-it").Return("", nil).Once()
	err := mgr.doPurge(0)
	assert.NoError(s.T(), err)

	// Test failed purge
	r.On("RunCommand", "ceph", "osd", "purge", "osd.1", "--yes-i-really-mean-it").Return("", fmt.Errorf("purge failed")).Once()
	err = mgr.doPurge(1)
	assert.Error(s.T(), err)
}

// TestTestSafeStop tests OSD safe stop check
func (s *osdSuite) TestTestSafeStop() {
	mgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	mgr.runner = r

	// Test safe to stop
	r.On("RunCommand", "ceph", "osd", "ok-to-stop", "osd.0").Return("", nil).Once()
	result := mgr.testSafeStop([]int64{0})
	assert.True(s.T(), result)

	// Test not safe to stop
	r.On("RunCommand", "ceph", "osd", "ok-to-stop", "osd.1").Return("", fmt.Errorf("not safe")).Once()
	result = mgr.testSafeStop([]int64{1})
	assert.False(s.T(), result)

	// Test multiple OSDs
	r.On("RunCommand", "ceph", "osd", "ok-to-stop", "osd.0", "osd.1").Return("", nil).Once()
	result = mgr.testSafeStop([]int64{0, 1})
	assert.True(s.T(), result)
}

// TestTestSafeDestroy tests OSD safe destroy check
func (s *osdSuite) TestTestSafeDestroy() {
	mgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	mgr.runner = r

	// Test safe to destroy
	r.On("RunCommand", "ceph", "osd", "safe-to-destroy", "osd.0").Return("", nil).Once()
	result := mgr.testSafeDestroy(0)
	assert.True(s.T(), result)

	// Test not safe to destroy
	r.On("RunCommand", "ceph", "osd", "safe-to-destroy", "osd.1").Return("", fmt.Errorf("not safe")).Once()
	result = mgr.testSafeDestroy(1)
	assert.False(s.T(), result)
}

// TestReweightOSD tests OSD reweighting
func (s *osdSuite) TestReweightOSD() {
	mgr := NewOSDManager(nil)
	r := mocks.NewRunner(s.T())
	mgr.runner = r

	// Test successful reweight
	r.On("RunCommand", "ceph", "osd", "crush", "reweight", "osd.0", "0.000000").Return("", nil).Once()
	mgr.reweightOSD(context.Background(), 0, 0.0)

	// Test failed reweight (should only log warning, not return error)
	r.On("RunCommand", "ceph", "osd", "crush", "reweight", "osd.1", "1.000000").Return("", fmt.Errorf("reweight failed")).Once()
	mgr.reweightOSD(context.Background(), 1, 1.0)
}

// TestValidateAddOSDArgs tests OSD addition argument validation
func (s *osdSuite) TestValidateAddOSDArgs() {
	mgr := NewOSDManager(nil)

	// Test valid args
	data := types.DiskParameter{Path: "/dev/sda"}
	err := mgr.validateAddOSDArgs(data, nil, nil)
	assert.NoError(s.T(), err)

	// Test loopback with WAL (should fail)
	loopData := types.DiskParameter{LoopSize: 1024}
	wal := &types.DiskParameter{Path: "/dev/wal"}
	err = mgr.validateAddOSDArgs(loopData, wal, nil)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "loopback and WAL/DB are mutually exclusive")

	// Test loopback with DB (should fail)
	db := &types.DiskParameter{Path: "/dev/db"}
	err = mgr.validateAddOSDArgs(loopData, nil, db)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "loopback and WAL/DB are mutually exclusive")
}
