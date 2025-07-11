package ceph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
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
	userID := fmt.Sprintf("nfs.%s.%s", clusterID, hostname)
	user := fmt.Sprintf("client.%s", userID)
	r.On("RunCommand", []interface{}{"ceph", "auth", "get-or-create", user, "mon", "allow r", "osd", "allow rw pool=.nfs namespace=foo", "-o", keyringPath}...).Return("ok", nil).Once()

	// ensureNFSPools calls
	r.On("RunCommand", []interface{}{
		"rados", "ls", "--pool", ".nfs", "--all", "--create"}...).Return("ok", nil).Once()
	r.On("RunCommand", []interface{}{
		"rados", "ls", "--pool", ".nfs.metadata", "--all", "--create"}...).Return("ok", nil).Once()
	r.On("RunCommand", []interface{}{
		"ceph", "osd", "pool", "application", "enable", ".nfs", "cephfs"}...).Return("ok", nil).Once()
	r.On("RunCommand", []interface{}{
		"rados", "create", "--pool", ".nfs", "-N", clusterID, obj}...).Return("ok", nil).Once()

	// addNodeToSharedGraceMgmtDb call
	r.On("RunCommand", []interface{}{
		"ganesha-rados-grace", "--cephconf", cephconf, "--pool", ".nfs", "--ns", clusterID, "--userid", userID, "add", hostname}...).Return("ok", nil).Once()

	// startNFS call
	r.On("RunCommand", "snapctl", "start", "microceph.nfs", "--enable").Return("ok", nil).Once()

	// patch processExec
	processExec = r

	nfs := &NFSServicePlacement{
		ClusterID:    clusterID,
		BindAddress:  "0.0.0.0",
		BindPort:     2049,
		V4MinVersion: 2,
	}

	// function call
	err = EnableNFS(s.TestStateInterface, nfs, []string{})

	assert.NoError(s.T(), err)
}

func (s *NFSSuite) TestNfsVersionsStr() {
	versions := nfsVersionsStr(2)
	assert.Equal(s.T(), "2", versions)

	versions = nfsVersionsStr(0)
	assert.Equal(s.T(), "0,1,2", versions)
}

func (s *NFSSuite) TestDisableNFSErrorDB() {
	db := mocks.NewGroupedServiceQueryIntf(s.T())

	// ExistsOnHost call
	ctx := context.Background()
	clusterID := "foo"
	err := fmt.Errorf("I've been expecting you")
	db.On("ExistsOnHost", []interface{}{ctx, s.TestStateInterface, "nfs", clusterID}...).Return(false, err).Once()

	// patch GroupedServicesQuery
	originalDB := database.GroupedServicesQuery
	defer func() { database.GroupedServicesQuery = originalDB }()
	database.GroupedServicesQuery = db

	err = DisableNFS(ctx, s.TestStateInterface, clusterID)

	assert.ErrorContains(s.T(), err, "I've been expecting you")
}

func (s *NFSSuite) TestDisableNFSNotExists() {
	s.TestStateInterface = mocks.NewStateInterface(s.T())
	u := api.NewURL()
	state := &mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
	}
	s.TestStateInterface.On("ClusterState").Return(state)

	db := mocks.NewGroupedServiceQueryIntf(s.T())

	// ExistsOnHost call
	ctx := context.Background()
	clusterID := "foo"
	db.On("ExistsOnHost", []interface{}{ctx, s.TestStateInterface, "nfs", clusterID}...).Return(false, nil).Once()

	// patch GroupedServicesQuery
	originalDB := database.GroupedServicesQuery
	defer func() { database.GroupedServicesQuery = originalDB }()
	database.GroupedServicesQuery = db

	err := DisableNFS(ctx, s.TestStateInterface, clusterID)

	assert.ErrorContains(s.T(), err, "NFS service with ClusterID 'foo' not found on node")
}

func (s *NFSSuite) TestDisableNFS() {
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
	cephConf := filepath.Join(ganeshaConfDir, "ceph.conf")
	ganeshaConf := filepath.Join(ganeshaConfDir, "ganesha.conf")

	// Create the files, and expect them to be deleted.
	err = os.MkdirAll(ganeshaConfDir, 0744)
	assert.NoError(s.T(), err)

	files := []string{keyringPath, cephConf, ganeshaConf}
	for _, file := range files {
		_, err := os.Create(file)
		assert.NoError(s.T(), err)
		defer os.Remove(file)
	}

	db := mocks.NewGroupedServiceQueryIntf(s.T())

	// ExistsOnHost call
	ctx := context.Background()
	clusterID := "foo"
	db.On("ExistsOnHost", []interface{}{ctx, s.TestStateInterface, "nfs", clusterID}...).Return(true, nil).Once()

	// RemoveFromHost call
	db.On("RemoveForHost", []interface{}{ctx, s.TestStateInterface, "nfs", clusterID}...).Return(nil).Once()

	// patch GroupedServicesQuery
	originalDB := database.GroupedServicesQuery
	defer func() { database.GroupedServicesQuery = originalDB }()
	database.GroupedServicesQuery = db

	r := mocks.NewRunner(s.T())

	// stopNFS call
	r.On("RunCommand", "snapctl", "stop", "microceph.nfs", "--disable").Return("ok", nil).Once()

	// removeNodeFromSharedGraceMgmtDb call
	userID := fmt.Sprintf("nfs.%s.%s", clusterID, hostname)
	r.On("RunCommand", []interface{}{
		"ganesha-rados-grace", "--cephconf", cephConf, "--pool", ".nfs", "--ns", clusterID, "--userid", userID, "remove", hostname}...).Return("ok", nil).Once()

	// DeleteClientKey call
	clientUser := fmt.Sprintf("client.%s", userID)
	r.On("RunCommand", "ceph", "auth", "del", clientUser).Return("ok", nil).Once()

	// patch processExec
	processExec = r

	// function call
	err = DisableNFS(ctx, s.TestStateInterface, clusterID)

	assert.NoError(s.T(), err)

	for _, file := range files {
		_, err := os.Stat(file)
		assert.True(s.T(), os.IsNotExist(err))
	}
}

func (s *NFSSuite) TestStartNFS() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "snapctl", "start", "microceph.nfs", "--enable").Return("ok", nil).Once()

	// patch processExec
	processExec = r

	// function call
	err := startNFS()
	assert.NoError(s.T(), err)
}

func (s *NFSSuite) TestStopNFS() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "snapctl", "stop", "microceph.nfs", "--disable").Return("ok", nil).Once()

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
	r.On("RunCommand", []interface{}{"ceph", "auth", "get-or-create", "client.lish", "mon", "allow r", "osd", "allow rw pool=.nfs namespace=foo", "-o", keyringPath}...).Return("ok", nil).Once()

	// patch processExec
	processExec = r

	// function call
	err := createNFSKeyring(s.Tmp, "foo", "lish")

	assert.NoError(s.T(), err)
}

func (s *NFSSuite) TestEnsureNFSPoolsFailPool() {
	r := mocks.NewRunner(s.T())
	clusterID := "foo"

	// mocks and expectations
	expectedErr := fmt.Errorf("expected to fail")
	r.On("RunCommand", []interface{}{
		"rados", "ls", "--pool", ".nfs", "--all", "--create"}...).Return("", expectedErr).Once()

	// patch processExec
	processExec = r

	// function call
	err := ensureNFSPools(clusterID)

	assert.ErrorContains(s.T(), err, "expected to fail")
}

func (s *NFSSuite) TestEnsureNFSPoolsFailEnable() {
	r := mocks.NewRunner(s.T())
	clusterID := "foo"

	// mocks and expectations
	existsErr := fmt.Errorf("File exists")
	r.On("RunCommand", []interface{}{
		"rados", "ls", "--pool", ".nfs", "--all", "--create"}...).Return("", existsErr).Once()
	r.On("RunCommand", []interface{}{
		"rados", "ls", "--pool", ".nfs.metadata", "--all", "--create"}...).Return("ok", nil).Once()

	expectedErr := fmt.Errorf("expected to fail")
	r.On("RunCommand", []interface{}{
		"ceph", "osd", "pool", "application", "enable", ".nfs", "cephfs"}...).Return("", expectedErr).Once()

	// patch processExec
	processExec = r

	// function call
	err := ensureNFSPools(clusterID)

	assert.ErrorContains(s.T(), err, "expected to fail")
}

func (s *NFSSuite) TestEnsureNFSPoolsFailCreateObj() {
	r := mocks.NewRunner(s.T())
	clusterID := "foo"
	obj := "conf-nfs.foo"

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"rados", "ls", "--pool", ".nfs", "--all", "--create"}...).Return("ok", nil).Once()
	r.On("RunCommand", []interface{}{
		"rados", "ls", "--pool", ".nfs.metadata", "--all", "--create"}...).Return("ok", nil).Once()

	r.On("RunCommand", []interface{}{
		"ceph", "osd", "pool", "application", "enable", ".nfs", "cephfs"}...).Return("ok", nil).Once()

	expectedErr := fmt.Errorf("expected to fail")
	r.On("RunCommand", []interface{}{
		"rados", "create", "--pool", ".nfs", "-N", clusterID, obj}...).Return("", expectedErr).Once()

	// patch processExec
	processExec = r

	// function call
	err := ensureNFSPools(clusterID)

	assert.ErrorContains(s.T(), err, "expected to fail")
}

func (s *NFSSuite) TestEnsureNFSPools() {
	r := mocks.NewRunner(s.T())
	clusterID := "foo"
	obj := "conf-nfs.foo"

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"rados", "ls", "--pool", ".nfs", "--all", "--create"}...).Return("ok", nil).Once()
	r.On("RunCommand", []interface{}{
		"rados", "ls", "--pool", ".nfs.metadata", "--all", "--create"}...).Return("ok", nil).Once()

	r.On("RunCommand", []interface{}{
		"ceph", "osd", "pool", "application", "enable", ".nfs", "cephfs"}...).Return("ok", nil).Once()

	existsErr := fmt.Errorf("File exists")
	r.On("RunCommand", []interface{}{
		"rados", "create", "--pool", ".nfs", "-N", clusterID, obj}...).Return("", existsErr).Once()

	// patch processExec
	processExec = r

	// function call
	err := ensureNFSPools(clusterID)

	assert.NoError(s.T(), err)
}

func (s *NFSSuite) TestAddNodeToSharedGraceMgmtDb() {
	r := mocks.NewRunner(s.T())
	cephconf := "/foo/ceph.conf"
	clusterID := "foo"
	userID := "lish"
	node := "one"
	node2 := "two"

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"ganesha-rados-grace", "--cephconf", cephconf, "--pool", ".nfs", "--ns", clusterID, "--userid", userID, "add", node}...).Return("ok", nil).Once()

	expectedErr := fmt.Errorf("expected to fail")
	r.On("RunCommand", []interface{}{
		"ganesha-rados-grace", "--cephconf", cephconf, "--pool", ".nfs", "--ns", clusterID, "--userid", userID, "add", node2}...).Return("", expectedErr).Once()

	// patch processExec
	processExec = r

	// function call
	err := addNodeToSharedGraceMgmtDb(cephconf, clusterID, userID, node)

	assert.NoError(s.T(), err)

	// function call
	err = addNodeToSharedGraceMgmtDb(cephconf, clusterID, userID, node2)

	assert.ErrorContains(s.T(), err, "expected to fail")
}

func (s *NFSSuite) TestRemoveNodeFromSharedGraceMgmtDb() {
	r := mocks.NewRunner(s.T())
	cephconf := "/foo/ceph.conf"
	clusterID := "foo"
	userID := "lish"
	node := "one"
	node2 := "two"

	// mocks and expectations
	r.On("RunCommand", []interface{}{
		"ganesha-rados-grace", "--cephconf", cephconf, "--pool", ".nfs", "--ns", clusterID, "--userid", userID, "remove", node}...).Return("ok", nil).Once()

	expectedErr := fmt.Errorf("expected to fail")
	r.On("RunCommand", []interface{}{
		"ganesha-rados-grace", "--cephconf", cephconf, "--pool", ".nfs", "--ns", clusterID, "--userid", userID, "remove", node2}...).Return("", expectedErr).Once()

	// patch processExec
	processExec = r

	// function call
	err := removeNodeFromSharedGraceMgmtDb(cephconf, clusterID, userID, node)

	assert.NoError(s.T(), err)

	// function call
	err = removeNodeFromSharedGraceMgmtDb(cephconf, clusterID, userID, node2)

	assert.ErrorContains(s.T(), err, "expected to fail")
}
