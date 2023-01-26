package ceph

import (
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microcluster/state"
	"github.com/lxc/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"testing"
)

type bootstrapSuite struct {
	suite.Suite
	TestStateInterface *mocks.StateInterface
	tmp                string
}

func TestBootstrap(t *testing.T) {
	suite.Run(t, new(bootstrapSuite))
}

func cmdAny(cmd string, no int) []interface{} {
	any := make([]interface{}, no+1)
	for i := range any {
		any[i] = mock.Anything
	}
	any[0] = cmd
	return any
}

func copyTestConf(dir string, conf string) error {
	_, tfile, _, _ := runtime.Caller(0)
	pkgDir := path.Join(path.Dir(tfile), "..")

	source, err := os.ReadFile(filepath.Join(pkgDir, "tests", "testdata", conf))
	if err != nil {
		return err
	}
	err = os.WriteFile(filepath.Join(dir, "SNAP_DATA", "conf", conf), source, 0644)
	if err != nil {
		return err
	}
	return nil
}

// Expect: run ceph-authtool 3 times
func addCreateKeyringExpectations(r *mocks.Runner) {
	r.On("RunCommand", cmdAny("ceph-authtool", 8)...).Return("ok", nil).Once()
	r.On("RunCommand", cmdAny("ceph-authtool", 17)...).Return("ok", nil).Once()
	r.On("RunCommand", cmdAny("ceph-authtool", 3)...).Return("ok", nil).Once()
}

// Expect: run monmaptool 2 times
func addCreateMonMapExpectations(r *mocks.Runner) {
	r.On("RunCommand", cmdAny("monmaptool", 8)...).Return("ok", nil).Once()
	r.On("RunCommand", cmdAny("monmaptool", 17)...).Return("ok", nil).Once()
}

// Expect: run ceph-mon and snap start
func addInitMonExpectations(r *mocks.Runner) {
	r.On("RunCommand", cmdAny("ceph-mon", 9)...).Return("ok", nil).Once()
	r.On("RunCommand", cmdAny("snapctl", 3)...).Return("ok", nil).Once()
}

// Expect: run ceph and snap start
func addInitMgrExpectations(r *mocks.Runner) {
	r.On("RunCommand", cmdAny("ceph", 11)...).Return("ok", nil).Once()
	r.On("RunCommand", cmdAny("snapctl", 3)...).Return("ok", nil).Once()
}

// Expect: run ceph and snap start
func addInitMdsExpectations(r *mocks.Runner) {
	r.On("RunCommand", cmdAny("ceph", 13)...).Return("ok", nil).Once()
	r.On("RunCommand", cmdAny("snapctl", 3)...).Return("ok", nil).Once()
}

// Expect: run ceph and snap start
func addEnableMsgr2Expectations(r *mocks.Runner) {
	r.On("RunCommand", cmdAny("ceph", 2)...).Return("ok", nil).Once()
	r.On("RunCommand", cmdAny("snapctl", 3)...).Return("ok", nil).Once()
}

func (s *bootstrapSuite) SetupTest() {
	tmp, err := os.MkdirTemp("", "microceph-test")
	if err != nil {
		s.T().Fatal("error creating tmp:", err)
	}

	s.tmp = tmp
	for _, d := range []string{"SNAP_DATA", "SNAP_COMMON"} {
		p := filepath.Join(tmp, d)
		err = os.MkdirAll(p, 0770)
		if err != nil {
			s.T().Fatal("error creating dir:", err)
		}
		os.Setenv(d, p)
	}
	for _, d := range []string{"SNAP_DATA/conf", "SNAP_DATA/run", "SNAP_COMMON/data", "SNAP_COMMON/logs"} {
		p := filepath.Join(tmp, d)
		err = os.Mkdir(p, 0770)
		if err != nil {
			s.T().Fatal("error creating dir:", err)
		}
	}

	for _, f := range []string{"ceph.client.admin.keyring", "ceph.conf"} {
		err = copyTestConf(tmp, f)
		if err != nil {
			s.T().Fatal("error copying testconf:", err)
		}
	}

	s.TestStateInterface = mocks.NewStateInterface(s.T())
	u := api.NewURL()
	state := &state.State{
		Address: func() *api.URL {
			return u
		},
		Name: func() string {
			return "foohost"
		},
		Database: nil,
	}
	s.TestStateInterface.On("ClusterState").Return(state)
}

func (s *bootstrapSuite) TearDownTest() {
	os.RemoveAll(s.tmp)
}

// Test a bootstrap run, mocking subprocess calls but without a live database
func (s *bootstrapSuite) TestBootstrap() {
	r := mocks.NewRunner(s.T())
	addCreateKeyringExpectations(r)
	addCreateMonMapExpectations(r)
	addInitMonExpectations(r)
	addInitMgrExpectations(r)
	addInitMdsExpectations(r)
	addEnableMsgr2Expectations(r)

	processExec = r

	err := Bootstrap(s.TestStateInterface)

	// we expect a missing database error
	assert.EqualError(s.T(), err, "no database")

}
