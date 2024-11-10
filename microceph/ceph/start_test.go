package ceph

import (
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

func (s *startSuite) TestStart() {
	r := mocks.NewRunner(s.T())

	addExpected(r)
	processExec = r

	err := PostRefresh()
	assert.NoError(s.T(), err)
}
