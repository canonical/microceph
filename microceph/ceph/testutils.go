package ceph

import (
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"os"
	"path"
	"path/filepath"
	"runtime"
)

type baseSuite struct {
	suite.Suite
	tmp string
}

// createTmp creates a temporary directory for the test
func (s *baseSuite) createTmp() {
	var err error
	s.tmp, err = os.MkdirTemp("", "microceph-test")
	if err != nil {
		s.T().Fatal("error creating tmp:", err)
	}
}

// copyCephConfigs copies a test config file to the test directory
func (s *baseSuite) copyCephConfigs() {
	var err error

	for _, d := range []string{"SNAP_DATA", "SNAP_COMMON"} {
		p := filepath.Join(s.tmp, d)
		err = os.MkdirAll(p, 0770)
		if err != nil {
			s.T().Fatal("error creating dir:", err)
		}
		os.Setenv(d, p)
	}
	for _, d := range []string{"SNAP_DATA/conf", "SNAP_DATA/run", "SNAP_COMMON/data", "SNAP_COMMON/logs"} {
		p := filepath.Join(s.tmp, d)
		err = os.Mkdir(p, 0770)
		if err != nil {
			s.T().Fatal("error creating dir:", err)
		}
	}

	for _, f := range []string{"ceph.client.admin.keyring", "ceph.conf"} {
		err = copyTestConf(s.tmp, f)
		if err != nil {
			s.T().Fatal("error copying testconf:", err)
		}
	}
}

func (s *baseSuite) SetupTest() {
	s.createTmp()
}

func (s *baseSuite) TearDownTest() {
	os.RemoveAll(s.tmp)
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
