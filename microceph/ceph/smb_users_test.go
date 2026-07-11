package ceph

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
)

type smbUsersSuite struct {
	tests.BaseSuite
}

func TestSMBUsersSuite(t *testing.T) {
	suite.Run(t, new(smbUsersSuite))
}

// stubImports replaces the passdb import seam with a recorder and makes
// retries immediate.
func (s *smbUsersSuite) stubImports(fail int) *[][3]string {
	calls := &[][3]string{}

	originalImport := smbPasswdImportFunc
	originalRetries := smbUserImportRetries
	originalInterval := smbUserImportInterval
	s.T().Cleanup(func() {
		smbPasswdImportFunc = originalImport
		smbUserImportRetries = originalRetries
		smbUserImportInterval = originalInterval
	})

	smbUserImportRetries = 3
	smbUserImportInterval = time.Duration(0)
	smbPasswdImportFunc = func(confPath, name, password string) error {
		*calls = append(*calls, [3]string{confPath, name, password})
		if len(*calls) <= fail {
			return fmt.Errorf("ctdb not ready")
		}
		return nil
	}

	return calls
}

func (s *smbUsersSuite) stubUserSource(doc string) {
	original := fetchSMBUserSourceFunc
	s.T().Cleanup(func() { fetchSMBUserSourceFunc = original })
	fetchSMBUserSourceFunc = func(uri string) ([]byte, error) {
		return []byte(doc), nil
	}
}

func (s *smbUsersSuite) TestSeedImportsAllUsers() {
	calls := s.stubImports(0)
	s.stubUserSource(`{"samba-container-config": "v0", "users": {"all_entries": [` +
		`{"name": "alice", "password": "pw1"}, {"name": "bob", "password": "pw2"}]}}`)

	spec := &types.SMBSpec{ClusterID: "dev", UserSources: []string{"rados:mon-config-key:smb/config/dev/users-groups.0.json"}}
	err := SeedSMBUsers(spec, NewSMBRenderParams("dev", "m1", true))
	assert.NoError(s.T(), err)

	assert.Len(s.T(), *calls, 2)
	assert.Equal(s.T(), "alice", (*calls)[0][1])
	assert.Equal(s.T(), "pw1", (*calls)[0][2])
	assert.Equal(s.T(), "bob", (*calls)[1][1])
}

func (s *smbUsersSuite) TestSeedRetriesWhileCTDBSettles() {
	calls := s.stubImports(2)
	s.stubUserSource(`{"users": {"all_entries": [{"name": "alice", "password": "pw"}]}}`)

	spec := &types.SMBSpec{ClusterID: "dev", UserSources: []string{"rados://.smb/dev/users.json"}}
	err := SeedSMBUsers(spec, NewSMBRenderParams("dev", "m1", true))
	assert.NoError(s.T(), err)
	assert.Len(s.T(), *calls, 3)
}

func (s *smbUsersSuite) TestSeedFailsAfterRetriesExhausted() {
	calls := s.stubImports(99)
	s.stubUserSource(`{"users": {"all_entries": [{"name": "alice", "password": "pw"}]}}`)

	spec := &types.SMBSpec{ClusterID: "dev", UserSources: []string{"rados://.smb/dev/users.json"}}
	err := SeedSMBUsers(spec, NewSMBRenderParams("dev", "m1", true))
	assert.ErrorContains(s.T(), err, "alice")
	assert.Len(s.T(), *calls, 3)
}

func (s *smbUsersSuite) TestSeedRejectsBadDocument() {
	s.stubImports(0)
	s.stubUserSource(`not json`)

	spec := &types.SMBSpec{ClusterID: "dev", UserSources: []string{"rados://.smb/dev/users.json"}}
	err := SeedSMBUsers(spec, NewSMBRenderParams("dev", "m1", true))
	assert.ErrorContains(s.T(), err, "cannot parse user source")
}

func (s *smbUsersSuite) TestFetchDispatchesMonConfigKey() {
	r := mocks.NewRunner(s.T())
	common.ProcessExec = r
	r.On("RunCommand", "ceph", "config-key", "get", "smb/config/dev/users-groups.0.json").Return(`{"users": {}}`, nil).Once()

	out, err := fetchSMBUserSource("rados:mon-config-key:smb/config/dev/users-groups.0.json")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), `{"users": {}}`, string(out))
}

func (s *smbUsersSuite) TestFetchDispatchesRADOSURI() {
	r := mocks.NewRunner(s.T())
	common.ProcessExec = r
	r.On("RunCommand", "rados", "get", "--pool", ".smb", "-N", "dev", "users.json", "-").Return(`{"users": {}}`, nil).Once()

	out, err := fetchSMBUserSource("rados://.smb/dev/users.json")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), `{"users": {}}`, string(out))
}
