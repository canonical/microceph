package ceph

import (
	"os"
	"testing"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type RbdMirrorSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestRbdMirror(t *testing.T) {
	suite.Run(t, new(RbdMirrorSuite))
}

func (ks *RbdMirrorSuite) SetupTest() {
	ks.BaseSuite.SetupTest()
	ks.CopyCephConfigs()
}

func (ks *RbdMirrorSuite) TestVerbosePoolStatus() {
	r := mocks.NewRunner(ks.T())

	output, _ := os.ReadFile("./test_assets/rbd_mirror_verbose_pool_status.json")

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "status", "pool", "--verbose", "--format", "json"}...).Return(string(output), nil).Once()
	processExec = r

	// Method call
	resp, err := GetRbdMirrorVerbosePoolStatus("pool", "", "")
	assert.NoError(ks.T(), err)
	assert.Equal(ks.T(), resp.Name, "pool")
}

func (ks *RbdMirrorSuite) TestPoolStatus() {
	r := mocks.NewRunner(ks.T())

	output, _ := os.ReadFile("./test_assets/rbd_mirror_pool_status.json")

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "status", "pool", "--format", "json"}...).Return(string(output), nil).Once()
	processExec = r

	// Method call
	resp, err := GetRbdMirrorPoolStatus("pool", "", "")
	assert.NoError(ks.T(), err)
	assert.Equal(ks.T(), resp.Health, RbdReplicationHealth("OK"))
	assert.Equal(ks.T(), resp.DaemonHealth, RbdReplicationHealth("OK"))
	assert.Equal(ks.T(), resp.ImageHealth, RbdReplicationHealth("OK"))
}

func (ks *RbdMirrorSuite) TestImageStatus() {
	r := mocks.NewRunner(ks.T())

	output, _ := os.ReadFile("./test_assets/rbd_mirror_image_status.json")

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "image", "status", "pool/image_one", "--format", "json"}...).Return(string(output), nil).Once()
	processExec = r

	// Method call
	resp, err := GetRbdMirrorImageStatus("pool", "image_one", "", "")
	assert.NoError(ks.T(), err)
	assert.Equal(ks.T(), resp.Name, "image_one")
	assert.Equal(ks.T(), resp.IsPrimary, true)
}

func (ks *RbdMirrorSuite) TestPoolInfo() {
	r := mocks.NewRunner(ks.T())

	output, _ := os.ReadFile("./test_assets/rbd_mirror_pool_info.json")

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"rbd", "mirror", "pool", "info", "pool", "--format", "json"}...).Return(string(output), nil).Once()
	processExec = r

	// Method call
	resp, err := GetRbdMirrorPoolInfo("pool", "", "")
	assert.NoError(ks.T(), err)
	assert.Equal(ks.T(), resp.Mode, types.RbdResourcePool)
	assert.Equal(ks.T(), resp.LocalSiteName, "magical")
	assert.Equal(ks.T(), resp.Peers[0].RemoteName, "simple")
}
