package ceph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
)

type controlReadinessSuite struct {
	suite.Suite
}

func TestControlReadiness(t *testing.T) {
	suite.Run(t, new(controlReadinessSuite))
}

// fixturePath returns the absolute path to a readiness test fixture under
// ceph/test_assets/readiness.
func (s *controlReadinessSuite) fixturePath(name string) string {
	return filepath.Join("test_assets", "readiness", name)
}

// loadFixture reads a test fixture file, failing the test on error.
func (s *controlReadinessSuite) loadFixture(name string) string {
	data, err := os.ReadFile(s.fixturePath(name))
	if err != nil {
		s.T().Fatalf("failed to read fixture %s: %v", name, err)
	}
	return string(data)
}

// withMockRunner installs a mocks.Runner as common.ProcessExec and returns it
// so the test can register expectations. The original ProcessExec is restored
// via t.Cleanup so the mock never leaks into other tests in the package.
func withMockRunner(t *testing.T) *mocks.Runner {
	orig := common.ProcessExec
	t.Cleanup(func() { common.ProcessExec = orig })
	r := mocks.NewRunner(t)
	common.ProcessExec = r
	return r
}

// --- monInQuorum tests ---

func (s *controlReadinessSuite) TestMonInQuorumMemberPresent() {
	r := withMockRunner(s.T())
	monStat := s.loadFixture("mon_stat_quorum.json")
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "stat", "-f", "json").
		Return(monStat, nil).Once()

	ok, err := monInQuorum(context.Background(), "mds-verify-b")
	assert.NoError(s.T(), err)
	assert.True(s.T(), ok, "member in quorum must be viable")
}

func (s *controlReadinessSuite) TestMonInQuorumMemberAbsent() {
	r := withMockRunner(s.T())
	monStat := s.loadFixture("mon_stat_quorum.json")
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "stat", "-f", "json").
		Return(monStat, nil).Once()

	ok, err := monInQuorum(context.Background(), "mds-verify-c")
	assert.NoError(s.T(), err)
	assert.False(s.T(), ok, "member not in quorum must not be viable")
}

func (s *controlReadinessSuite) TestMonInQuorumCommandError() {
	r := withMockRunner(s.T())
	r.On("RunCommandContext", mock.Anything, "ceph", "mon", "stat", "-f", "json").
		Return("", fmt.Errorf("ceph command failed")).Once()

	ok, err := monInQuorum(context.Background(), "mds-verify-a")
	assert.Error(s.T(), err)
	assert.False(s.T(), ok)
}

// --- mgrActiveOrStandby tests ---

func (s *controlReadinessSuite) TestMgrActiveOrStandbyActiveMember() {
	r := withMockRunner(s.T())
	mgrMeta := s.loadFixture("mgr_metadata.json")
	r.On("RunCommandContext", mock.Anything, "ceph", "mgr", "metadata", "-f", "json").
		Return(mgrMeta, nil).Once()

	ok, err := mgrActiveOrStandby(context.Background(), "mds-verify-a")
	assert.NoError(s.T(), err)
	assert.True(s.T(), ok, "active MGR member must be viable")
}

func (s *controlReadinessSuite) TestMgrActiveOrStandbyStandbyMember() {
	r := withMockRunner(s.T())
	mgrMeta := s.loadFixture("mgr_metadata.json")
	r.On("RunCommandContext", mock.Anything, "ceph", "mgr", "metadata", "-f", "json").
		Return(mgrMeta, nil).Once()

	ok, err := mgrActiveOrStandby(context.Background(), "mds-verify-b")
	assert.NoError(s.T(), err)
	assert.True(s.T(), ok, "standby MGR member must be viable")
}

func (s *controlReadinessSuite) TestMgrActiveOrStandbyUnknownMember() {
	r := withMockRunner(s.T())
	mgrMeta := s.loadFixture("mgr_metadata.json")
	r.On("RunCommandContext", mock.Anything, "ceph", "mgr", "metadata", "-f", "json").
		Return(mgrMeta, nil).Once()

	ok, err := mgrActiveOrStandby(context.Background(), "mds-verify-c")
	assert.NoError(s.T(), err)
	assert.False(s.T(), ok, "unknown MGR member must not be viable")
}

func (s *controlReadinessSuite) TestMgrActiveOrStandbyCommandError() {
	r := withMockRunner(s.T())
	r.On("RunCommandContext", mock.Anything, "ceph", "mgr", "metadata", "-f", "json").
		Return("", fmt.Errorf("ceph command failed")).Once()

	ok, err := mgrActiveOrStandby(context.Background(), "mds-verify-a")
	assert.Error(s.T(), err)
	assert.False(s.T(), ok)
}

// --- mdsUp tests ---

func (s *controlReadinessSuite) TestMdsUpActiveMember() {
	r := withMockRunner(s.T())
	mdsStat := s.loadFixture("mds_stat_active.json")
	r.On("RunCommandContext", mock.Anything, "ceph", "mds", "stat", "-f", "json").
		Return(mdsStat, nil).Once()

	ok, err := mdsUp(context.Background(), "mds-verify-b")
	assert.NoError(s.T(), err)
	assert.True(s.T(), ok, "active MDS member must be viable")
}

func (s *controlReadinessSuite) TestMdsUpStandbyMember() {
	r := withMockRunner(s.T())
	mdsStat := s.loadFixture("mds_stat_active.json")
	r.On("RunCommandContext", mock.Anything, "ceph", "mds", "stat", "-f", "json").
		Return(mdsStat, nil).Once()

	ok, err := mdsUp(context.Background(), "mds-verify-a")
	assert.NoError(s.T(), err)
	assert.True(s.T(), ok, "standby MDS member must be viable")
}

func (s *controlReadinessSuite) TestMdsUpUnknownMember() {
	r := withMockRunner(s.T())
	mdsStat := s.loadFixture("mds_stat_active.json")
	r.On("RunCommandContext", mock.Anything, "ceph", "mds", "stat", "-f", "json").
		Return(mdsStat, nil).Once()

	ok, err := mdsUp(context.Background(), "mds-verify-c")
	assert.NoError(s.T(), err)
	assert.False(s.T(), ok, "unknown MDS member must not be viable")
}

func (s *controlReadinessSuite) TestMdsUpNoFilesystemStandby() {
	r := withMockRunner(s.T())
	mdsStat := s.loadFixture("mds_stat_nofs.json")
	r.On("RunCommandContext", mock.Anything, "ceph", "mds", "stat", "-f", "json").
		Return(mdsStat, nil).Once()

	ok, err := mdsUp(context.Background(), "mds-verify-a")
	assert.NoError(s.T(), err)
	assert.True(s.T(), ok, "standby MDS with no filesystem must still be viable")
}

func (s *controlReadinessSuite) TestMdsUpCommandError() {
	r := withMockRunner(s.T())
	r.On("RunCommandContext", mock.Anything, "ceph", "mds", "stat", "-f", "json").
		Return("", fmt.Errorf("ceph command failed")).Once()

	ok, err := mdsUp(context.Background(), "mds-verify-a")
	assert.Error(s.T(), err)
	assert.False(s.T(), ok)
}
