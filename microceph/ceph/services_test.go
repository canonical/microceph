package ceph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type servicesSuite struct {
	baseSuite
	// TestStateInterface *mocks.StateInterface
}

func TestServices(t *testing.T) {
	suite.Run(t, new(servicesSuite))
}

// Set up test suite
func (s *servicesSuite) SetupTest() {
	s.baseSuite.SetupTest()
}

func addOsdDumpExpectations(r *mocks.Runner) {
	osdDumpObj := "{\"osds\":[{\"up\":1,\"uuid\":\"bfbbd27a-472f-4771-a6f7-7c5db9803d41\"}]}"
	osdDump, _ := json.Marshal(osdDumpObj)

	// Expect osd service worker query
	r.On("RunCommand", []interface{}{
		"ceph", "osd", "dump", "-f", "json-pretty",
	}...).Return(string(osdDump[:]), nil).Twice()
}

func addMonDumpExpectations(r *mocks.Runner) {
	monDumpObj := "{\"mons\":[{\"name\":\"bfbbd27a\"}]}"
	monDump, _ := json.Marshal(monDumpObj)

	// Expect mon service worker query
	r.On("RunCommand", []interface{}{
		"ceph", "mon", "dump", "-f", "json-pretty",
	}...).Return(string(monDump[:]), nil).Twice()
}

func addServiceRestartExpectations(r *mocks.Runner, services []string) {
	for _, service := range services {
		r.On("RunCommand", []interface{}{
			"snapctl", "restart", fmt.Sprintf("microceph.%s", service),
		}...).Return("ok", nil).Once()
	}
}

func (s *servicesSuite) TestRestartInvalidService() {
	err := RestartCephService("InvalidService")
	assert.ErrorContains(s.T(), err, "No handler defined")
}

func (s *servicesSuite) TestRestartServiceWorkerSuccess() {
	ts := []string{"mon", "osd"} // test services

	r := mocks.NewRunner(s.T())
	addMonDumpExpectations(r)
	addOsdDumpExpectations(r)
	addServiceRestartExpectations(r, ts)
	processExec = r

	// Handler is defined for both mon and osd services.
	err := RestartCephServices(ts)
	assert.Equal(s.T(), err, nil)
}

// TestCleanService tests the cleanService function.
func (s *servicesSuite) TestCleanService() {
	s.copyCephConfigs()
	svcPath := filepath.Join(s.tmp, "SNAP_COMMON", "data", "mon", "ceph-foo-host")
	os.MkdirAll(svcPath, 0770)
	cleanService("foo-host", "mon")
	assert.NoDirExists(s.T(), svcPath)
}
