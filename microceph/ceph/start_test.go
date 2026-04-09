package ceph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

type startSuite struct {
	tests.BaseSuite
}

func TestStart(t *testing.T) {
	suite.Run(t, new(startSuite))
}

func addExpected(r *mocks.Runner) {
	version := `ceph version 19.2.0 (e7ad5345525c7aa95470c26863873b581076945d) squid (stable)`
	versionsJson := `{
    "mon": {
        "ceph version 18.2.4 (e7ad5345525c7aa95470c26863873b581076945d) reef (stable)": 1
    },
    "mgr": {
        "ceph version 18.2.4 (e7ad5345525c7aa95470c26863873b581076945d) reef (stable)": 1
    },
    "osd": {
        "ceph version 18.2.4 (e7ad5345525c7aa95470c26863873b581076945d) reef (stable)": 4
    },
    "mds": {
        "ceph version 18.2.4 (e7ad5345525c7aa95470c26863873b581076945d) reef (stable)": 1
    },
    "overall": {
        "ceph version 18.2.4 (e7ad5345525c7aa95470c26863873b581076945d) reef (stable)": 7
    }
}`
	osdDump := `{"require_osd_release": "reef"}`

	r.On("RunCommand", "ceph", "-v").Return(version, nil).Once()
	r.On("RunCommand", "ceph", "versions").Return(versionsJson, nil).Once()
	r.On("RunCommand", "ceph", "osd", "dump", "-f", "json").Return(osdDump, nil).Once()
	r.On("RunCommand", "ceph", "osd", "require-osd-release",
		"squid", "--yes-i-really-mean-it").Return("ok", nil).Once()
}

func (s *startSuite) TestStartOSDReleaseUpdate() {
	r := mocks.NewRunner(s.T())

	addExpected(r)
	common.ProcessExec = r

	err := PostRefresh()
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

func (s *startSuite) TestInvalidVersionString() {
	r := mocks.NewRunner(s.T())
	// only expect the version command, others shouldnt be reached
	r.On("RunCommand", "ceph", "-v").Return("invalid version", nil).Once()
	common.ProcessExec = r

	err := PostRefresh()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "invalid version string")
	r.AssertExpectations(s.T())
}

func (s *startSuite) TestMultipleVersionsPresent() {
	versionRetrySleep = func(_ time.Duration) {}
	s.T().Cleanup(func() { versionRetrySleep = time.Sleep })
	
	r := mocks.NewRunner(s.T())
	version := `ceph version 19.2.0 (e7ad5345525c7aa95470c26863873b581076945d) squid (stable)`
	versionsJson := `{
    "mon": {
        "ceph version 18.2.4 reef (stable)": 1,
        "ceph version 19.2.0 squid (stable)": 1
    },
    "overall": {
        "ceph version 18.2.4 reef (stable)": 1,
        "ceph version 19.2.0 squid (stable)": 1
    }
}`

	r.On("RunCommand", "ceph", "-v").Return(version, nil).Once()
	r.On("RunCommand", "ceph", "versions").Return(versionsJson, nil).Times(10)
	common.ProcessExec = r

	err := PostRefresh()
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

func (s *startSuite) TestNoOSDVersions() {
	r := mocks.NewRunner(s.T())
	version := `ceph version 19.2.0 (e7ad5345525c7aa95470c26863873b581076945d) squid (stable)`
	versionsJson := `{
    "mon": {
        "ceph version 19.2.0 squid (stable)": 1
    },
    "overall": {
        "ceph version 19.2.0 squid (stable)": 1
    }
}`

	r.On("RunCommand", "ceph", "-v").Return(version, nil).Once()
	r.On("RunCommand", "ceph", "versions").Return(versionsJson, nil).Once()
	common.ProcessExec = r

	err := PostRefresh()
	assert.NoError(s.T(), err) // no OSD versions, so no update required
	r.AssertExpectations(s.T())
}

func (s *startSuite) TestOSDReleaseUpToDate() {
	r := mocks.NewRunner(s.T())
	version := `ceph version 19.2.0 (e7ad5345525c7aa95470c26863873b581076945d) squid (stable)`
	versionsJson := `{
    "mon": {
        "ceph version 19.2.0 squid (stable)": 1
    },
    "osd": {
        "ceph version 19.2.0 squid (stable)": 1
    },
    "overall": {
        "ceph version 19.2.0 squid (stable)": 2
    }
}`
	osdDump := `{"require_osd_release": "squid"}`

	r.On("RunCommand", "ceph", "-v").Return(version, nil).Once()
	r.On("RunCommand", "ceph", "versions").Return(versionsJson, nil).Once()
	r.On("RunCommand", "ceph", "osd", "dump", "-f", "json").Return(osdDump, nil).Once()
	common.ProcessExec = r

	err := PostRefresh()
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

func (s *startSuite) TestOSDReleaseUpdateFails() {
	r := mocks.NewRunner(s.T())
	version := `ceph version 19.2.0 (e7ad5345525c7aa95470c26863873b581076945d) squid (stable)`
	versionsJson := `{
    "mon": {
        "ceph version 19.2.0 squid (stable)": 1
    },
    "osd": {
        "ceph version 19.2.0 squid (stable)": 1
    },
    "overall": {
        "ceph version 19.2.0 squid (stable)": 2
    }
}`
	osdDump := `{"require_osd_release": "reef"}`

	r.On("RunCommand", "ceph", "-v").Return(version, nil).Once()
	r.On("RunCommand", "ceph", "versions").Return(versionsJson, nil).Once()
	r.On("RunCommand", "ceph", "osd", "dump", "-f", "json").Return(osdDump, nil).Once()
	r.On("RunCommand", "ceph", "osd", "require-osd-release", "squid", "--yes-i-really-mean-it").
		Return("", errors.New("update failed")).Once()
	common.ProcessExec = r

	err := PostRefresh()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "OSD release update failed")
	r.AssertExpectations(s.T())
}

func (s *startSuite) TestCephVersionCommandFails() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "-v").Return("", errors.New("command failed")).Once()
	common.ProcessExec = r

	err := PostRefresh()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "version check failed")
	r.AssertExpectations(s.T())
}

// --- reEnableServices tests ---

// setupReEnable wires the common mocks for reEnableServices tests:
// a Runner for snapctl calls, a function-variable override for
// getServicesForHost, and a mock for GroupedServicesQuery.
func (s *startSuite) setupReEnable(
	services []database.Service,
	grouped []database.GroupedService,
) *mocks.Runner {
	orig := getServicesForHost
	s.T().Cleanup(func() { getServicesForHost = orig })
	getServicesForHost = func(_ context.Context, _ interfaces.StateInterface, _ string) ([]database.Service, error) {
		return services, nil
	}

	origGS := database.GroupedServicesQuery
	s.T().Cleanup(func() { database.GroupedServicesQuery = origGS })
	gsDb := mocks.NewGroupedServiceQueryIntf(s.T())
	gsDb.On("GetGroupedServicesOnHost", mock.Anything, mock.Anything).Return(grouped, nil).Maybe()
	database.GroupedServicesQuery = gsDb

	r := mocks.NewRunner(s.T())
	common.ProcessExec = r

	return r
}

func (s *startSuite) newState() *mocks.StateInterface {
	si := mocks.NewStateInterface(s.T())
	si.On("ClusterState").Return(&mocks.MockState{ClusterName: "node1"}).Maybe()
	return si
}

func (s *startSuite) TestReEnableNoServicesSkips() {
	// No services registered → early return, no snapctl calls at all.
	s.setupReEnable(nil, nil)
	reEnableServices(context.Background(), s.newState())
}

func (s *startSuite) TestReEnableInactiveServiceRestarted() {
	r := s.setupReEnable(
		[]database.Service{{Service: "mon", Member: "node1"}},
		nil,
	)
	// mon is inactive → gets restarted.
	r.On("RunCommand", "snapctl", "services", "microceph.mon").Return("inactive", nil).Once()
	r.On("RunCommand", "snapctl", "start", "microceph.mon", "--enable").Return("ok", nil).Once()
	// OSD active.
	r.On("RunCommand", "snapctl", "services", "microceph.osd").Return("active", nil).Once()

	reEnableServices(context.Background(), s.newState())
}

func (s *startSuite) TestReEnableOSDRestarted() {
	r := s.setupReEnable(
		[]database.Service{{Service: "mon", Member: "node1"}},
		nil,
	)
	r.On("RunCommand", "snapctl", "services", "microceph.mon").Return("active", nil).Once()
	// OSD is inactive → gets restarted.
	r.On("RunCommand", "snapctl", "services", "microceph.osd").Return("inactive", nil).Once()
	r.On("RunCommand", "snapctl", "start", "microceph.osd", "--enable").Return("ok", nil).Once()

	reEnableServices(context.Background(), s.newState())
}

func (s *startSuite) TestReEnableGroupedServiceRestarted() {
	r := s.setupReEnable(
		[]database.Service{{Service: "mon", Member: "node1"}},
		[]database.GroupedService{
			{Service: "nfs", GroupID: "g1", Member: "node1"},
			{Service: "nfs", GroupID: "g2", Member: "node1"}, // duplicate — should be skipped
		},
	)
	r.On("RunCommand", "snapctl", "services", "microceph.mon").Return("active", nil).Once()
	r.On("RunCommand", "snapctl", "services", "microceph.osd").Return("active", nil).Once()
	// nfs checked once (deduplicated), inactive → restarted.
	r.On("RunCommand", "snapctl", "services", "microceph.nfs").Return("inactive", nil).Once()
	r.On("RunCommand", "snapctl", "start", "microceph.nfs", "--enable").Return("ok", nil).Once()

	reEnableServices(context.Background(), s.newState())
}

// TestShouldSkipMonitorRefresh is a regression test for issue #556.
func (s *startSuite) TestShouldSkipMonitorRefresh() {
	// First run should always trigger UpdateConfig.
	assert.False(s.T(), shouldSkipMonitorRefresh(true, nil, []string{"node1"}))

	// Unchanged monitor list should not trigger UpdateConfig.
	assert.True(s.T(), shouldSkipMonitorRefresh(false, []string{"node1"}, []string{"node1"}))

	// New monitor added should trigger UpdateConfig.
	assert.False(s.T(), shouldSkipMonitorRefresh(false, []string{"node1"}, []string{"node1", "node2"}))
}

// --- migrateStaleRunDir tests ---

// writeConf is a helper that writes content to a file under the test SNAP_DATA/conf dir.
func (s *startSuite) writeConf(relPath, content string) string {
	full := filepath.Join(s.Tmp, "SNAP_DATA", "conf", relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		s.T().Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		s.T().Fatal(err)
	}
	return full
}

func (s *startSuite) TestMigrateStaleRunDirNoConfFiles() {
	// Neither radosgw.conf nor ganesha.conf exist — should be a silent no-op.
	s.CopyCephConfigs()
	migrateStaleRunDir()
}

func (s *startSuite) TestMigrateStaleRunDirRGWAlreadyCorrect() {
	s.CopyCephConfigs()
	correctRunDir := filepath.Join(s.Tmp, "current", "run")
	content := fmt.Sprintf("run dir = %s\n", correctRunDir)
	s.writeConf("radosgw.conf", content)

	migrateStaleRunDir()

	assert.Equal(s.T(), content, s.ReadCephConfig("radosgw.conf"))
}

func (s *startSuite) TestMigrateStaleRunDirRGWFixed() {
	s.CopyCephConfigs()
	// Write radosgw.conf with the old revision-specific path.
	staleRunDir := filepath.Join(s.Tmp, "SNAP_DATA", "run")
	s.writeConf("radosgw.conf", fmt.Sprintf(`# Generated by MicroCeph, DO NOT EDIT.
[global]
mon host = 10.0.0.1
run dir = %s
auth allow insecure global id reclaim = false
`, staleRunDir))

	migrateStaleRunDir()

	result := s.ReadCephConfig("radosgw.conf")
	correctRunDir := filepath.Join(s.Tmp, "current", "run")
	assert.Contains(s.T(), result, "run dir = "+correctRunDir)
	assert.NotContains(s.T(), result, staleRunDir)
}

func (s *startSuite) TestMigrateStaleRunDirGaneshaFixed() {
	s.CopyCephConfigs()
	// Write ganesha.conf with the old revision-specific path.
	staleRunDir := filepath.Join(s.Tmp, "SNAP_DATA", "run")
	s.writeConf("ganesha/ganesha.conf", fmt.Sprintf(`# Generated by MicroCeph, DO NOT EDIT.
NFS_KRB5 {
	CCacheDir = "%s/ganesha";
}
`, staleRunDir))

	migrateStaleRunDir()

	result := s.ReadCephConfig("ganesha/ganesha.conf")
	correctRunDir := filepath.Join(s.Tmp, "current", "run")
	assert.Contains(s.T(), result, `CCacheDir = "`+correctRunDir+`/ganesha";`)
	assert.NotContains(s.T(), result, staleRunDir)
}

func (s *startSuite) TestMigrateStaleRunDirGaneshaAlreadyCorrect() {
	s.CopyCephConfigs()
	correctRunDir := filepath.Join(s.Tmp, "current", "run")
	content := fmt.Sprintf("\tCCacheDir = \"%s/ganesha\";\n", correctRunDir)
	s.writeConf("ganesha/ganesha.conf", content)

	migrateStaleRunDir()

	assert.Equal(s.T(), content, s.ReadCephConfig("ganesha/ganesha.conf"))
}
