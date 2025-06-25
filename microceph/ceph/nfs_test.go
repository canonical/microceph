package ceph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type NFSSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestNFS(t *testing.T) {
	suite.Run(t, new(NFSSuite))
}

func (s *NFSSuite) SetupTest() {
	s.BaseSuite.SetupTest()
}

func (s *NFSSuite) TestEnableNFS() {
	hostname, err := os.Hostname()
	assert.NoError(s.T(), err)

	original := constants.GetPathConst
	defer func() { constants.GetPathConst = original }()

	constants.GetPathConst = func() constants.PathConst {
		return constants.PathConst{
			ConfPath: s.Tmp,
		}
	}

	ganeshaConfDir := filepath.Join(s.Tmp, "ganesha")
	keyringPath := filepath.Join(ganeshaConfDir, "keyring")
	cephconf := filepath.Join(ganeshaConfDir, "ceph.conf")

	clusterID := "foo"
	obj := "conf-nfs.foo"
	r := mocks.NewRunner(s.T())

	// createNFSKeyring call
	r.On("RunCommand", []interface{}{"ceph", "auth", "get-or-create", "client.nfs.ganesha", "mon", "allow r", "osd", "allow rw pool=.nfs namespace=foo", "-o", keyringPath}...).Return("ok", nil).Once()

	// ensureNFSPool calls
	r.On("RunCommand", []interface{}{
		"rados", "ls", "--pool", ".nfs", "--all", "--create"}...).Return("ok", nil).Once()
	r.On("RunCommand", []interface{}{
		"ceph", "osd", "pool", "application", "enable", ".nfs", "nfs"}...).Return("ok", nil).Once()
	r.On("RunCommand", []interface{}{
		"rados", "create", "-p", ".nfs", "-N", clusterID, obj}...).Return("ok", nil).Once()

	// addNodeToSharedGraceMgmtDb call
	r.On("RunCommand", []interface{}{
		"ganesha-rados-grace", "--cephconf", cephconf, "--pool", ".nfs", "--ns", clusterID, "--userid", "nfs.ganesha", "add", hostname}...).Return("ok", nil).Once()

	// startNFS call
	r.On("RunCommand", "snapctl", "start", "microceph.nfs-ganesha", "--enable").Return("ok", nil).Once()

	// patch processExec
	processExec = r

	// function call
	err = EnableNFS(s.TestStateInterface, clusterID, "0.0.0.0", 2049, 2, []string{})

	assert.NoError(s.T(), err)
}

func (s *NFSSuite) TestDisableNFS() {
	original := constants.GetPathConst
	defer func() { constants.GetPathConst = original }()

	constants.GetPathConst = func() constants.PathConst {
		return constants.PathConst{
			ConfPath: s.Tmp,
		}
	}

	ganeshaConfDir := filepath.Join(s.Tmp, "ganesha")
	keyringPath := filepath.Join(ganeshaConfDir, "keyring")
	cephConf := filepath.Join(ganeshaConfDir, "ceph.conf")
	ganeshaConf := filepath.Join(ganeshaConfDir, "ganesha.conf")

	// Create the files, and expect them to be deleted.
	err := os.MkdirAll(ganeshaConfDir, 0744)
	assert.NoError(s.T(), err)

	files := []string{keyringPath, cephConf, ganeshaConf}
	for _, file := range files {
		_, err := os.Create(file)
		assert.NoError(s.T(), err)
		defer os.Remove(file)
	}

	u := api.NewURL()
	state := &mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
	}
	s.TestStateInterface = mocks.NewStateInterface(s.T())
	s.TestStateInterface.On("ClusterState").Return(state)

	r := mocks.NewRunner(s.T())

	// stopNFS call
	r.On("RunCommand", "snapctl", "stop", "microceph.nfs-ganesha", "--disable").Return("ok", nil).Once()

	// patch processExec
	processExec = r

	// function call
	err = DisableNFS(context.Background(), s.TestStateInterface, "foo")

	assert.NoError(s.T(), err)

	for _, file := range files {
		_, err := os.Stat(file)
		assert.True(s.T(), os.IsNotExist(err))
	}
}

func (s *NFSSuite) TestStartNFS() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "snapctl", "start", "microceph.nfs-ganesha", "--enable").Return("ok", nil).Once()

	// patch processExec
	processExec = r

	// function call
	err := startNFS()
	assert.NoError(s.T(), err)
}

func (s *NFSSuite) TestStopNFS() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "snapctl", "stop", "microceph.nfs-ganesha", "--disable").Return("ok", nil).Once()

	// patch processExec
	processExec = r

	// function call
	err := stopNFS()
	assert.NoError(s.T(), err)
}

func (s *NFSSuite) TestCreateNFSKeyring() {
	keyringPath := filepath.Join(s.Tmp, "keyring")
	r := mocks.NewRunner(s.T())

	// mocks and expectations
	r.On("RunCommand", []interface{}{"ceph", "auth", "get-or-create", "client.nfs.ganesha", "mon", "allow r", "osd", "allow rw pool=.nfs namespace=foo", "-o", keyringPath}...).Return("ok", nil).Once()

	// patch processExec
	processExec = r

	// function call
	err := createNFSKeyring(s.Tmp, "foo")

	assert.NoError(s.T(), err)
}

func (s *NFSSuite) TestEnsureNFSPoolFailPool() {
	r := mocks.NewRunner(s.T())
	clusterID := "foo"

	// mocks and expectations
	expectedErr := fmt.Errorf("expected to fail")
	r.On("RunCommand", []interface{}{
		"rados", "ls", "--pool", ".nfs", "--all", "--create"}...).Return("", expectedErr).Once()

	// patch processExec
	processExec = r

	// function call
	err := ensureNFSPool(clusterID)

	assert.ErrorContains(s.T(), err, "expected to fail")
}

func (s *NFSSuite) TestEnsureNFSPoolFailEnable() {
	r := mocks.NewRunner(s.T())
	clusterID := "foo"

	// mocks and expectations
	existsErr := fmt.Errorf("File exists")
	r.On("RunCommand", []interface{}{
		"rados", "ls", "--pool", ".nfs", "--all", "--create"}...).Return("", existsErr).Once()

	expectedErr := fmt.Errorf("expected to fail")
	r.On("RunCommand", []interface{}{
		"ceph", "osd", "pool", "application", "enable", ".nfs", "nfs"}...).Return("", expectedErr).Once()

	// patch processExec
	processExec = r

	// function call
	err := ensureNFSPool(clusterID)

	assert.ErrorContains(s.T(), err, "expected to fail")
}

func (s *NFSSuite) TestEnsureNFSPoolFailCreateObj() {
	r := mocks.NewRunner(s.T())
	clusterID := "foo"
	obj := "conf-nfs.foo"

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"rados", "ls", "--pool", ".nfs", "--all", "--create"}...).Return("ok", nil).Once()

	r.On("RunCommand", []interface{}{
		"ceph", "osd", "pool", "application", "enable", ".nfs", "nfs"}...).Return("ok", nil).Once()

	expectedErr := fmt.Errorf("expected to fail")
	r.On("RunCommand", []interface{}{
		"rados", "create", "-p", ".nfs", "-N", clusterID, obj}...).Return("", expectedErr).Once()

	// patch processExec
	processExec = r

	// function call
	err := ensureNFSPool(clusterID)

	assert.ErrorContains(s.T(), err, "expected to fail")
}

func (s *NFSSuite) TestEnsureNFSPool() {
	r := mocks.NewRunner(s.T())
	clusterID := "foo"
	obj := "conf-nfs.foo"

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"rados", "ls", "--pool", ".nfs", "--all", "--create"}...).Return("ok", nil).Once()

	r.On("RunCommand", []interface{}{
		"ceph", "osd", "pool", "application", "enable", ".nfs", "nfs"}...).Return("ok", nil).Once()

	existsErr := fmt.Errorf("File exists")
	r.On("RunCommand", []interface{}{
		"rados", "create", "-p", ".nfs", "-N", clusterID, obj}...).Return("", existsErr).Once()

	// patch processExec
	processExec = r

	// function call
	err := ensureNFSPool(clusterID)

	assert.NoError(s.T(), err)
}

func (s *NFSSuite) TestAddNodeToSharedGraceMgmtDb() {
	r := mocks.NewRunner(s.T())
	cephconf := "/foo/ceph.conf"
	clusterID := "lish"
	node := "one"
	node2 := "two"

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"ganesha-rados-grace", "--cephconf", cephconf, "--pool", ".nfs", "--ns", clusterID, "--userid", "nfs.ganesha", "add", node}...).Return("ok", nil).Once()

	expectedErr := fmt.Errorf("expected to fail")
	r.On("RunCommand", []interface{}{
		"ganesha-rados-grace", "--cephconf", cephconf, "--pool", ".nfs", "--ns", clusterID, "--userid", "nfs.ganesha", "add", node2}...).Return("", expectedErr).Once()

	// patch processExec
	processExec = r

	// function call
	err := addNodeToSharedGraceMgmtDb(cephconf, clusterID, node)

	assert.NoError(s.T(), err)

	// function call
	err = addNodeToSharedGraceMgmtDb(cephconf, clusterID, node2)

	assert.ErrorContains(s.T(), err, "expected to fail")
}
