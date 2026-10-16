package ceph

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
)

const smbTestContainer = `{"shares":{"files":{"options":{"vfs objects":"ceph_new","ceph_new:proxy":"no"}}}}`
const smbTestPayload = `{"cluster_id":"files","config_uri":"rados://.smb/files/config.smb"}`

// preserveSMBTestGlobal restores only the dependency a test actually overrides.
func preserveSMBTestGlobal[T any](t *testing.T, target *T) {
	t.Helper()
	before := *target
	t.Cleanup(func() { *target = before })
}

func smbTestPaths(t *testing.T, withData bool) (root, conf, runtime string) {
	t.Helper()
	root = t.TempDir()
	conf, runtime = filepath.Join(root, "conf"), filepath.Join(root, "samba")
	paths := constants.PathConst{ConfPath: conf}
	if withData {
		paths.DataPath = filepath.Join(root, "data")
	}
	preserveSMBTestGlobal(t, &constants.GetPathConst)
	constants.GetPathConst = func() constants.PathConst { return paths }
	return root, conf, runtime
}

func smbTestWrite(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
}

func smbTestSource(t *testing.T, data string) {
	t.Helper()
	preserveSMBTestGlobal(t, &fetchSMBSourceFunc)
	fetchSMBSourceFunc = func(context.Context, string) ([]byte, error) { return []byte(data), nil }
}

func smbTestRunner(t *testing.T) *mocks.Runner {
	t.Helper()
	preserveSMBTestGlobal(t, &common.ProcessExec)
	runner := mocks.NewRunner(t)
	common.ProcessExec = runner
	return runner
}

func smbTestCommand(runner *mocks.Runner, command string, args ...string) *mock.Call {
	values := []any{mock.Anything, command}
	for _, arg := range args {
		values = append(values, arg)
	}
	return runner.On("RunCommandContext", values...)
}

func smbTestGroupedDB(t *testing.T) *mocks.GroupedServiceQueryIntf {
	t.Helper()
	preserveSMBTestGlobal(t, &database.GroupedServicesQuery)
	db := mocks.NewGroupedServiceQueryIntf(t)
	database.GroupedServicesQuery = db
	return db
}
