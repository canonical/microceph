package ceph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type servicesSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestServices(t *testing.T) {
	suite.Run(t, new(servicesSuite))
}

// Set up test suite
func (s *servicesSuite) SetupTest() {
	s.BaseSuite.SetupTest()

	s.TestStateInterface = mocks.NewStateInterface(s.T())
	u := api.NewURL()
	state := mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
	}
	s.TestStateInterface.On("ClusterState").Return(&state).Maybe()
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
	services := types.Services{}
	err := RestartCephService(services, "InvalidService", "foohost")
	assert.ErrorContains(s.T(), err, "no handler defined")
}

func (s *servicesSuite) TestRestartServiceWorkerSuccess() {
	ts := []string{"mon", "osd"} // test services

	r := mocks.NewRunner(s.T())
	addMonDumpExpectations(r)
	addOsdDumpExpectations(r)
	addServiceRestartExpectations(r, ts)
	common.ProcessExec = r

	services := types.Services{
		types.Service{Service: "mon", Location: "foohost"},
		types.Service{Service: "osd", Location: "foohost"},
	}

	// Handler is defined for both mon and osd services.
	err := RestartCephService(services, "mon", "foohost")
	assert.NoError(s.T(), err)

	err = RestartCephService(services, "osd", "foohost")
	assert.NoError(s.T(), err)
}

// TestCleanService tests the cleanService function.
func (s *servicesSuite) TestCleanService() {
	s.CopyCephConfigs()
	svcPath := filepath.Join(s.Tmp, "SNAP_COMMON", "data", "mon", "ceph-foo-host")
	_ = os.MkdirAll(svcPath, 0770)
	_ = cleanService("foo-host", "mon")
	assert.NoDirExists(s.T(), svcPath)
}

// installDeleteServiceRecorder replaces the config render and the external
// deletion phases and records their exact order. The originals are restored
// after each test.
func installDeleteServiceRecorder(t *testing.T, stopErr error) *[]string {
	origUpdateConfig := updateConfigFunc
	origEnsureMonAbsent := ensureMonAbsentFunc
	origEnsureMgrAbsent := ensureMgrAbsentFunc
	origEnsureMdsAbsent := ensureMdsAbsentFunc
	origSnapStop := snapStopFunc
	origCleanService := cleanServiceFunc
	origRemoveServiceDatabase := removeServiceDatabaseFunc
	t.Cleanup(func() {
		updateConfigFunc = origUpdateConfig
		ensureMonAbsentFunc = origEnsureMonAbsent
		ensureMgrAbsentFunc = origEnsureMgrAbsent
		ensureMdsAbsentFunc = origEnsureMdsAbsent
		snapStopFunc = origSnapStop
		cleanServiceFunc = origCleanService
		removeServiceDatabaseFunc = origRemoveServiceDatabase
	})

	events := []string{}
	updateConfigFunc = func(_ context.Context, _ interfaces.StateInterface) error {
		events = append(events, "config")
		return nil
	}
	ensureMonAbsentFunc = func(_ context.Context, hostname string) error {
		events = append(events, "monmap:"+hostname)
		return nil
	}
	ensureMgrAbsentFunc = func(_ context.Context, hostname string) error {
		events = append(events, "mgrmap:"+hostname)
		return nil
	}
	ensureMdsAbsentFunc = func(_ context.Context, hostname string) error {
		events = append(events, "mdsmap:"+hostname)
		return nil
	}
	snapStopFunc = func(service string, disable bool) error {
		events = append(events, fmt.Sprintf("stop:%s:%t", service, disable))
		return stopErr
	}
	cleanServiceFunc = func(hostname, service string) error {
		events = append(events, "clean:"+service+":"+hostname)
		return nil
	}
	removeServiceDatabaseFunc = func(_ context.Context, _ interfaces.StateInterface, service string) error {
		events = append(events, "db:"+service)
		return nil
	}
	return &events
}

func (s *servicesSuite) TestDeleteMonRemovesMembershipBeforeStoppingDaemon() {
	events := installDeleteServiceRecorder(s.T(), nil)

	err := DeleteService(context.Background(), s.TestStateInterface, "mon")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), []string{
		"config",
		"monmap:foohost",
		"stop:mon:true",
		"clean:mon:foohost",
		"db:mon",
	}, *events)
}

func (s *servicesSuite) TestDeleteMonMembershipFailureLeavesDaemonRunning() {
	events := installDeleteServiceRecorder(s.T(), nil)
	membershipErr := errors.New("monmap unavailable")
	ensureMonAbsentFunc = func(_ context.Context, hostname string) error {
		*events = append(*events, "monmap:"+hostname)
		return membershipErr
	}

	err := DeleteService(context.Background(), s.TestStateInterface, "mon")
	assert.ErrorIs(s.T(), err, membershipErr)
	assert.Equal(s.T(), []string{"config", "monmap:foohost"}, *events)
}

func (s *servicesSuite) TestDeleteMonResumesAfterStopFailure() {
	events := installDeleteServiceRecorder(s.T(), nil)
	stopErr := errors.New("snap stop failed")
	stopAttempts := 0
	snapStopFunc = func(service string, disable bool) error {
		*events = append(*events, fmt.Sprintf("stop:%s:%t", service, disable))
		stopAttempts++
		if stopAttempts == 1 {
			return stopErr
		}
		return nil
	}

	// The first pass models a committed monmap update followed by a local stop
	// failure. The retained DB row causes a retry; ensureMonAbsent is idempotent
	// and local teardown then finishes.
	err := DeleteService(context.Background(), s.TestStateInterface, "mon")
	assert.ErrorIs(s.T(), err, stopErr)
	err = DeleteService(context.Background(), s.TestStateInterface, "mon")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), []string{
		"config",
		"monmap:foohost",
		"stop:mon:true",
		"config",
		"monmap:foohost",
		"stop:mon:true",
		"clean:mon:foohost",
		"db:mon",
	}, *events)
}

func (s *servicesSuite) TestDeleteMgrEvictsMapAfterStoppingDaemon() {
	events := installDeleteServiceRecorder(s.T(), nil)

	err := DeleteService(context.Background(), s.TestStateInterface, "mgr")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), []string{
		"config",
		"stop:mgr:true",
		"mgrmap:foohost",
		"clean:mgr:foohost",
		"db:mgr",
	}, *events)
}

func (s *servicesSuite) TestDeleteMdsEvictsMapAfterStoppingDaemon() {
	events := installDeleteServiceRecorder(s.T(), nil)

	err := DeleteService(context.Background(), s.TestStateInterface, "mds")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), []string{
		"config",
		"stop:mds:true",
		"mdsmap:foohost",
		"clean:mds:foohost",
		"db:mds",
	}, *events)
}

func (s *servicesSuite) TestDeleteMgrMapFailureLeavesCleanupPending() {
	events := installDeleteServiceRecorder(s.T(), nil)
	mapErr := errors.New("mgr map unavailable")
	ensureMgrAbsentFunc = func(_ context.Context, hostname string) error {
		*events = append(*events, "mgrmap:"+hostname)
		return mapErr
	}

	err := DeleteService(context.Background(), s.TestStateInterface, "mgr")
	assert.ErrorIs(s.T(), err, mapErr)
	// The daemon is stopped and eviction attempted, but on-disk and DB cleanup
	// must not run so a retry resumes teardown.
	assert.Equal(s.T(), []string{
		"config",
		"stop:mgr:true",
		"mgrmap:foohost",
	}, *events)
}

func (s *servicesSuite) TestDeleteConfigRenderFailureAbortsBeforeTouchingCeph() {
	events := installDeleteServiceRecorder(s.T(), nil)
	renderErr := errors.New("failed to locate IP on public network")
	updateConfigFunc = func(_ context.Context, _ interfaces.StateInterface) error {
		*events = append(*events, "config")
		return renderErr
	}

	for _, service := range []string{"mon", "mgr", "mds"} {
		*events = nil
		err := DeleteService(context.Background(), s.TestStateInterface, service)
		assert.ErrorIs(s.T(), err, renderErr)
		// With a stale ceph.conf the ensure*Absent phases would hang on an
		// unreachable monitor, so nothing may run before a successful render.
		assert.Equal(s.T(), []string{"config"}, *events)
	}
}

func (s *servicesSuite) TestDeleteClientServiceSkipsConfigRender() {
	events := installDeleteServiceRecorder(s.T(), nil)

	// Services without a Ceph map eviction never run ceph commands during
	// teardown. They must not gain the render's public network requirement,
	// which would block `cluster remove` on a node that lost its Ceph NIC.
	err := DeleteService(context.Background(), s.TestStateInterface, "rbd-mirror")
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), []string{
		"stop:rbd-mirror:true",
		"clean:rbd-mirror:foohost",
		"db:rbd-mirror",
	}, *events)
}
