package ceph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

type smbKeyringSuite struct {
	tests.BaseSuite
}

func TestSMBKeyringSuite(t *testing.T) {
	suite.Run(t, new(smbKeyringSuite))
}

func (s *smbKeyringSuite) spec() *types.SMBSpec {
	var spec types.SMBSpec
	err := json.Unmarshal([]byte(validSMBPayload), &spec)
	assert.NoError(s.T(), err)
	return &spec
}

func (s *smbKeyringSuite) TestSMBDaemonEntity() {
	assert.Equal(s.T(), "client.smb.dev.smbdev-1", SMBDaemonEntity("dev", "smbdev-1"))
}

func (s *smbKeyringSuite) TestPoolCapsSMBPool() {
	caps := smbPoolCapsFromURI("rados://.smb/dev/scc.dev.json")
	assert.Equal(s.T(), []string{
		"allow r pool=.smb",
		"allow rwx pool=.smb namespace=dev object_prefix cluster.meta.",
	}, caps)
}

func (s *smbKeyringSuite) TestPoolCapsSMBPoolNoNamespace() {
	caps := smbPoolCapsFromURI("rados://.smb/scc.json")
	assert.Equal(s.T(), []string{
		"allow r pool=.smb",
		"allow rwx pool=.smb namespace= object_prefix cluster.meta.",
	}, caps)
}

func (s *smbKeyringSuite) TestPoolCapsForeignPool() {
	assert.Equal(s.T(), []string{"allow r pool=users"}, smbPoolCapsFromURI("rados://users/x/y.json"))
}

func (s *smbKeyringSuite) TestPoolCapsNonRADOS() {
	assert.Empty(s.T(), smbPoolCapsFromURI("http://example.com/x.json"))
}

func (s *smbKeyringSuite) TestOSDCaps() {
	spec := s.spec()
	spec.UserSources = append(spec.UserSources, "rados://users/x.json")

	caps := smbOSDCaps(spec)
	assert.Equal(s.T(),
		"allow r pool=.smb, allow r pool=users, allow rwx pool=.smb namespace=dev object_prefix cluster.meta.",
		caps)
}

func (s *smbKeyringSuite) TestMonCaps() {
	assert.Equal(s.T(),
		`allow r, allow command "config-key get" with "key" prefix "smb/config/dev/"`,
		smbMonCaps("dev"))
}

// writeKeyringOnGet makes an "auth get ... -o <path>" expectation create the
// output file, as the real ceph CLI would.
func writeKeyringOnGet(s *smbKeyringSuite, r *mocks.Runner, entity string) {
	r.On("RunCommand", "ceph", "auth", "get", entity, "-o", mock.Anything).Run(func(args mock.Arguments) {
		path := args.Get(5).(string)
		err := os.WriteFile(path, []byte("["+entity+"]\n\tkey = secret\n"), 0600)
		assert.NoError(s.T(), err)
	}).Return("", nil).Once()
}

func (s *smbKeyringSuite) TestEnsureSMBKeyrings() {
	confDir := s.T().TempDir()
	spec := s.spec()
	spec.IncludeCephUsers = []string{"client.data1"}

	entity := "client.smb.dev.host1"
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "auth", "get-or-create", entity).Return("", nil).Once()
	r.On("RunCommand", "ceph", "auth", "caps", entity,
		"mon", smbMonCaps("dev"), "osd", smbOSDCaps(spec)).Return("", nil).Once()
	writeKeyringOnGet(s, r, entity)
	writeKeyringOnGet(s, r, "client.data1")
	common.ProcessExec = r

	err := EnsureSMBKeyrings(spec, "host1", confDir)
	assert.NoError(s.T(), err)

	for _, name := range []string{"ceph.client.smb.dev.host1.keyring", "ceph.client.data1.keyring"} {
		info, err := os.Stat(filepath.Join(confDir, name))
		assert.NoError(s.T(), err, name)
		assert.Equal(s.T(), os.FileMode(0600), info.Mode().Perm(), name)
	}
}

func (s *smbKeyringSuite) TestEnsureSMBKeyringsRejectsBadEntity() {
	confDir := s.T().TempDir()
	spec := s.spec()
	spec.IncludeCephUsers = []string{"client.foo/../../etc"}

	// No Runner expectations: validation must fail before any ceph call.
	common.ProcessExec = mocks.NewRunner(s.T())

	err := EnsureSMBKeyrings(spec, "host1", confDir)
	assert.ErrorContains(s.T(), err, "not a valid cephx entity")
}

func (s *smbKeyringSuite) TestRemoveSMBKeyrings() {
	confDir := s.T().TempDir()
	spec := s.spec()
	spec.IncludeCephUsers = []string{"client.data1"}

	for _, name := range []string{"ceph.client.smb.dev.host1.keyring", "ceph.client.data1.keyring"} {
		err := os.WriteFile(filepath.Join(confDir, name), []byte("k"), 0600)
		assert.NoError(s.T(), err)
	}

	r := mocks.NewRunner(s.T())
	// The daemon key is deleted from ceph; include_ceph_users keys belong
	// to mgr/smb and only their fetched files are removed.
	r.On("RunCommand", "ceph", "auth", "del", "client.smb.dev.host1").Return("", nil).Once()
	common.ProcessExec = r

	err := RemoveSMBKeyrings(spec, "host1", confDir)
	assert.NoError(s.T(), err)

	entries, err := os.ReadDir(confDir)
	assert.NoError(s.T(), err)
	assert.Empty(s.T(), entries)
}
