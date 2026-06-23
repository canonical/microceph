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
	"github.com/canonical/microceph/microceph/constants"
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

// --- updateRadosGWMonHost tests (issue #766) ---

// radosgwConfForTest writes a radosgw.conf with the given mon host line and the
// usual RGW frontend settings, returning the rendered content.
func (s *startSuite) radosgwConfForTest(monHostLine string) string {
	content := fmt.Sprintf(`# Generated by MicroCeph, DO NOT EDIT.
[global]
%s
run dir = /var/snap/microceph/current/run
auth allow insecure global id reclaim = false

[client.radosgw.gateway]
rgw init timeout = 1200
rgw frontends = beast port=80
`, monHostLine)
	s.writeConf("radosgw.conf", content)
	return content
}

func (s *startSuite) TestUpdateRadosGWMonHostRewritesStaleLine() {
	s.CopyCephConfigs()
	s.radosgwConfForTest("mon host = 192.168.123.11,192.168.123.12")

	err := updateRadosGWMonHost(constants.GetPathConst().ConfPath,
		[]string{"192.168.123.11", "192.168.123.12", "192.168.123.13"})
	assert.NoError(s.T(), err)

	result := s.ReadCephConfig("radosgw.conf")
	assert.Contains(s.T(), result, "mon host = 192.168.123.11,192.168.123.12,192.168.123.13")
	// The unpersisted RGW frontend settings must be preserved.
	assert.Contains(s.T(), result, "rgw frontends = beast port=80")
	assert.Contains(s.T(), result, "rgw init timeout = 1200")
}

func (s *startSuite) TestUpdateRadosGWMonHostAlreadyCurrent() {
	s.CopyCephConfigs()
	content := s.radosgwConfForTest("mon host = 192.168.123.11,192.168.123.12")

	err := updateRadosGWMonHost(constants.GetPathConst().ConfPath,
		[]string{"192.168.123.11", "192.168.123.12"})
	assert.NoError(s.T(), err)

	// No line changed — the file must be byte-for-byte unchanged (no spurious rewrite).
	assert.Equal(s.T(), content, s.ReadCephConfig("radosgw.conf"))
}

func (s *startSuite) TestUpdateRadosGWMonHostNoFileIsNoOp() {
	s.CopyCephConfigs()
	// radosgw.conf does not exist (RGW not enabled).
	err := updateRadosGWMonHost(constants.GetPathConst().ConfPath,
		[]string{"192.168.123.11"})
	assert.NoError(s.T(), err)

	_, statErr := os.Stat(filepath.Join(s.Tmp, "SNAP_DATA", "conf", "radosgw.conf"))
	assert.True(s.T(), os.IsNotExist(statErr))
}

func (s *startSuite) TestUpdateRadosGWMonHostIPv6() {
	s.CopyCephConfigs()
	s.radosgwConfForTest("mon host = [fd00::11]")

	// formatIPv6 wraps bare IPv6 monitors in brackets; the refresh must write
	// them exactly as supplied (consistent with ceph.conf).
	err := updateRadosGWMonHost(constants.GetPathConst().ConfPath,
		[]string{"[fd00::11]", "[fd00::12]"})
	assert.NoError(s.T(), err)

	result := s.ReadCephConfig("radosgw.conf")
	assert.Contains(s.T(), result, "mon host = [fd00::11],[fd00::12]")
}

// TestUpdateRadosGWMonHostNoMonHostLineIsNoOp verifies that a radosgw.conf with
// no `mon host =` line (e.g. hand-edited) is left untouched. fixConfigLine only
// rewrites existing matching lines; it never inserts new ones, and re-deriving
// the full file is out of scope because the RGW frontend settings are not
// persisted. The file must be byte-for-byte unchanged.
func (s *startSuite) TestUpdateRadosGWMonHostNoMonHostLineIsNoOp() {
	s.CopyCephConfigs()
	content := `# Generated by MicroCeph, DO NOT EDIT.
[global]
run dir = /var/snap/microceph/current/run
auth allow insecure global id reclaim = false

[client.radosgw.gateway]
rgw frontends = beast port=80
`
	s.writeConf("radosgw.conf", content)

	err := updateRadosGWMonHost(constants.GetPathConst().ConfPath,
		[]string{"192.168.123.11", "192.168.123.12"})
	assert.NoError(s.T(), err)

	assert.Equal(s.T(), content, s.ReadCephConfig("radosgw.conf"))
}

// TestUpdateRadosGWMonHostEmptyMonitorsIsNoOp verifies that an empty/unknown
// monitor set never wipes an existing `mon host =` line. A transient gap in the
// monitor list (e.g. truststore not yet populated, DB read race) must not
// rewrite a healthy line to the blank "mon host = ", which would permanently
// break RGW's ability to reach any monitor.
func (s *startSuite) TestUpdateRadosGWMonHostEmptyMonitorsIsNoOp() {
	s.CopyCephConfigs()
	content := s.radosgwConfForTest("mon host = 192.168.123.11,192.168.123.12")

	err := updateRadosGWMonHost(constants.GetPathConst().ConfPath, []string{})
	assert.NoError(s.T(), err)

	// The file must be byte-for-byte unchanged — no wipe to a blank mon host line.
	assert.Equal(s.T(), content, s.ReadCephConfig("radosgw.conf"))
}

// TestUpdateRadosGWMonHostUnchangedSetDifferentOrder verifies that a stable
// monitor set presented in a different order does not trigger a rewrite.
// getMonitorsFromConfig iterates a map (randomized order in Go); without order
// normalization the exact-byte equality check would spuriously rewrite
// radosgw.conf on every UpdateConfig tick. The file must be byte-for-byte
// unchanged.
func (s *startSuite) TestUpdateRadosGWMonHostUnchangedSetDifferentOrder() {
	s.CopyCephConfigs()
	content := s.radosgwConfForTest("mon host = 192.168.123.11,192.168.123.12,192.168.123.13")

	// Same set, reversed order — must be treated as already-current.
	err := updateRadosGWMonHost(constants.GetPathConst().ConfPath,
		[]string{"192.168.123.13", "192.168.123.12", "192.168.123.11"})
	assert.NoError(s.T(), err)

	assert.Equal(s.T(), content, s.ReadCephConfig("radosgw.conf"))
}
