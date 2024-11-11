package ceph

import (
	"errors"
	"testing"

	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/stretchr/testify/assert"
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
	processExec = r

	err := PostRefresh()
	assert.NoError(s.T(), err)
	r.AssertExpectations(s.T())
}

func (s *startSuite) TestInvalidVersionString() {
	r := mocks.NewRunner(s.T())
	// only expect the version command, others shouldnt be reached
	r.On("RunCommand", "ceph", "-v").Return("invalid version", nil).Once()
	processExec = r

	err := PostRefresh()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "invalid version string")
	r.AssertExpectations(s.T())
}

func (s *startSuite) TestMultipleVersionsPresent() {
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
	r.On("RunCommand", "ceph", "versions").Return(versionsJson, nil).Times(3)
	processExec = r

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
	processExec = r

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
	processExec = r

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
	processExec = r

	err := PostRefresh()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "OSD release update failed")
	r.AssertExpectations(s.T())
}

func (s *startSuite) TestCephVersionCommandFails() {
	r := mocks.NewRunner(s.T())
	r.On("RunCommand", "ceph", "-v").Return("", errors.New("command failed")).Once()
	processExec = r

	err := PostRefresh()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "version check failed")
	r.AssertExpectations(s.T())
}
