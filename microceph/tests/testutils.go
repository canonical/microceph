package tests

import (
	"os"
	"path"
	"path/filepath"
	"runtime"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

type BaseSuite struct {
	suite.Suite
	Tmp string
}

// CreateTmp creates a temporary directory for the test
func (s *BaseSuite) CreateTmp() {
	var err error
	s.Tmp, err = os.MkdirTemp("", "microceph-test")
	if err != nil {
		s.T().Fatal("error creating Tmp:", err)
	}
}

// copyCephConfigs copies a test config file to the test directory
func (s *BaseSuite) CopyCephConfigs() {
	var err error

	for _, d := range []string{"SNAP_DATA", "SNAP_COMMON"} {
		p := filepath.Join(s.Tmp, d)
		err = os.MkdirAll(p, 0770)
		if err != nil {
			s.T().Fatal("error creating dir:", err)
		}
		os.Setenv(d, p)
	}
	for _, d := range []string{"SNAP_DATA/conf", "SNAP_DATA/run", "SNAP_COMMON/data", "SNAP_COMMON/logs"} {
		p := filepath.Join(s.Tmp, d)
		err = os.Mkdir(p, 0770)
		if err != nil {
			s.T().Fatal("error creating dir:", err)
		}
	}

	for _, f := range []string{"ceph.client.admin.keyring", "ceph.conf"} {
		err = CopyTestConf(s.Tmp, f)
		if err != nil {
			s.T().Fatal("error copying testconf:", err)
		}
	}
}

// readCephConfig reads a config file from the test directory
func (s *BaseSuite) ReadCephConfig(conf string) string {
	// Read the config file
	data, _ := os.ReadFile(filepath.Join(s.Tmp, "SNAP_DATA", "conf", conf))
	return string(data)
}

func (s *BaseSuite) SetupTest() {
	s.CreateTmp()
	os.Setenv("TEST_ROOT_PATH", s.Tmp)
	os.MkdirAll(filepath.Join(s.Tmp, "proc"), 0775)
}

func (s *BaseSuite) TearDownTest() {
	os.RemoveAll(s.Tmp)
}

func CmdAny(cmd string, no int) []interface{} {
	any := make([]interface{}, no+1)
	for i := range any {
		any[i] = mock.Anything
	}
	any[0] = cmd
	return any
}

func CopyTestConf(dir string, conf string) error {
	_, tfile, _, _ := runtime.Caller(0)
	pkgDir := path.Join(path.Dir(tfile), "..")

	source, err := os.ReadFile(filepath.Join(pkgDir, "tests", "testdata", conf))
	if err != nil {
		return err
	}
	err = os.WriteFile(filepath.Join(dir, "SNAP_DATA", "conf", conf), source, 0640)
	if err != nil {
		return err
	}
	return nil
}
