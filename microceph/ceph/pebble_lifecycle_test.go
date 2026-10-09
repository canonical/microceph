package ceph

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
)

type lifecycleRunner struct {
	run func(string, ...string) (string, error)
}

// RunCommand records a lifecycle boundary without running host commands.
func (r lifecycleRunner) RunCommand(name string, args ...string) (string, error) {
	return r.run(name, args...)
}

// RunCommandContext shares the controlled command implementation.
func (r lifecycleRunner) RunCommandContext(_ context.Context, name string, args ...string) (string, error) {
	return r.run(name, args...)
}

// TestSnapCheckActiveRequiresChild rejects backoff behind an active Snap app.
func TestSnapCheckActiveRequiresChild(t *testing.T) {
	for _, app := range []string{"daemon", "mon", "mgr", "mds", "rgw", "nfs", "rbd-mirror", "cephfs-mirror"} {
		t.Run(app, func(t *testing.T) {
			t.Setenv("SNAP", t.TempDir())
			old := common.ProcessExec
			t.Cleanup(func() { common.ProcessExec = old })
			childErr := errors.New("child is in backoff")
			checked := false
			common.ProcessExec = lifecycleRunner{run: func(name string, args ...string) (string, error) {
				if name == "snapctl" {
					return "microceph." + app + " enabled active", nil
				}
				require.Equal(t, filepath.Join(os.Getenv("SNAP"), "bin", "microceph-pebble"), name)
				require.Equal(t, []string{"status", app}, args)
				checked = true
				return "", childErr
			}}
			require.ErrorIs(t, snapCheckActive(app), childErr)
			require.True(t, checked)
		})
	}
}

// TestSnapCheckActiveKeepsOSDAggregate preserves the multi-OSD startup check.
func TestSnapCheckActiveKeepsOSDAggregate(t *testing.T) {
	old := common.ProcessExec
	t.Cleanup(func() { common.ProcessExec = old })
	common.ProcessExec = lifecycleRunner{run: func(name string, args ...string) (string, error) {
		require.Equal(t, "snapctl", name)
		return "microceph.osd enabled active", nil
	}}
	require.NoError(t, snapCheckActive("osd"))
}

// TestKillOSDUsesNamedSupervisorStop forbids a PID-regex stop fallback.
func TestKillOSDUsesNamedSupervisorStop(t *testing.T) {
	t.Setenv("SNAP", t.TempDir())
	stopErr := errors.New("exit was not verified")
	m := NewOSDManager(nil)
	m.runner = lifecycleRunner{run: func(name string, args ...string) (string, error) {
		require.Equal(t, filepath.Join(os.Getenv("SNAP"), "bin", "microceph-pebble"), name)
		require.Equal(t, []string{"osd-stop", "7"}, args)
		return "", stopErr
	}}
	require.ErrorIs(t, m.killOSD(7), stopErr)
}

// TestRemoveOSDStopFailurePreservesFence exercises the real removal caller.
func TestRemoveOSDStopFailurePreservesFence(t *testing.T) {
	t.Setenv("SNAP_COMMON", t.TempDir())
	path := getOSDDataPath(7)
	require.NoError(t, os.MkdirAll(path, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(path, "ready"), nil, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(path, "block"), []byte("retain storage"), 0600))
	oldRunner, oldQuery := common.ProcessExec, database.OSDQuery
	t.Cleanup(func() { common.ProcessExec, database.OSDQuery = oldRunner, oldQuery })
	query := mocks.NewOSDQueryInterface(t)
	database.OSDQuery = query
	query.On("HaveOSD", mock.Anything, mock.Anything, int64(7)).Return(true, nil).Once()
	state := mocks.NewStateInterface(t)
	state.On("ClusterState").Return(&mocks.MockState{}).Maybe()
	stopErr := errors.New("process is still running")
	stopped := false
	common.ProcessExec = lifecycleRunner{run: func(name string, args ...string) (string, error) {
		command := strings.Join(args, " ")
		if strings.Contains(command, "osd_pool_default_crush_rule") {
			return "0", nil
		}
		if command == "osd crush rule dump microceph_auto_host" {
			return `{"rule_id":1}`, nil
		}
		if command == "osd tree -f json" && !stopped {
			return `{"nodes":[]}`, nil
		}
		if name == "pkill" || filepath.Base(name) == "microceph-pebble" {
			stopped = true
			return "", stopErr
		}
		t.Errorf("continued after failed stop: %s %v", name, args)
		return "", errors.New("cleanup must not proceed")
	}}
	err := doRemoveOSD(context.Background(), state, 7, true)
	require.ErrorIs(t, err, stopErr)
	require.FileExists(t, filepath.Join(path, "ready.removing"))
	require.NoFileExists(t, filepath.Join(path, "ready"))
	require.FileExists(t, filepath.Join(path, "block"))
}

// TestPostPlacementWaitsForChild separates startup from sustained status checks.
func TestPostPlacementWaitsForChild(t *testing.T) {
	old := common.ProcessExec
	t.Cleanup(func() { common.ProcessExec = old })
	waitErr := errors.New("child never started")
	common.ProcessExec = lifecycleRunner{run: func(name string, args ...string) (string, error) {
		require.Equal(t, "microceph-pebble", filepath.Base(name))
		require.Equal(t, []string{"wait-ready", "mon"}, args)
		return "", waitErr
	}}
	require.ErrorIs(t, genericPostPlacementCheck("mon"), waitErr)
}

// TestSpawnOSDWaitsForNamedChild does not equate Snap start with child startup.
func TestSpawnOSDWaitsForNamedChild(t *testing.T) {
	old := common.ProcessExec
	t.Cleanup(func() { common.ProcessExec = old })
	waited := false
	runner := lifecycleRunner{run: func(name string, args ...string) (string, error) {
		if name == "snapctl" {
			require.Equal(t, []string{"restart", "--reload", "microceph.osd"}, args)
			return "", nil
		}
		require.Equal(t, []string{"wait-ready", "osd-7"}, args)
		waited = true
		return "", nil
	}}
	common.ProcessExec = runner
	m := NewOSDManager(nil)
	require.NoError(t, m.spawnOSD(7))
	require.True(t, waited, "outer start completion is not child startup")
}

type retainedFenceFS struct{ afero.Fs }

// Remove detects the transient loss of a pre-existing removal fence.
func (f retainedFenceFS) Remove(path string) error {
	if filepath.Base(path) == "ready.removing" {
		return errors.New("must never clear an existing removal fence")
	}
	return f.Fs.Remove(path)
}

// TestSuppressionNeverClearsExistingFence covers simultaneous ready markers.
func TestSuppressionNeverClearsExistingFence(t *testing.T) {
	m := NewOSDManager(nil)
	m.fs = retainedFenceFS{afero.NewMemMapFs()}
	path := getOSDDataPath(7)
	require.NoError(t, m.fs.MkdirAll(path, 0700))
	require.NoError(t, afero.WriteFile(m.fs, filepath.Join(path, "ready"), nil, 0600))
	require.NoError(t, afero.WriteFile(m.fs, filepath.Join(path, "ready.removing"), nil, 0600))
	_, _, err := m.suppressOSDAutostart(7)
	require.NoError(t, err)
}

// TestSuppressionFencesAnUnpublishedDirectory prevents late publication.
func TestSuppressionFencesAnUnpublishedDirectory(t *testing.T) {
	m := NewOSDManager(nil)
	m.fs = afero.NewMemMapFs()
	path := getOSDDataPath(7)
	require.NoError(t, m.fs.MkdirAll(path, 0700))
	_, _, err := m.suppressOSDAutostart(7)
	require.NoError(t, err)
	exists, err := afero.Exists(m.fs, filepath.Join(path, "ready.removing"))
	require.NoError(t, err)
	require.True(t, exists, "a late publication must encounter a fence")
}

// TestFailedAddRollbackPreservesStorageOnStopFailure includes generated WAL/DB.
func TestFailedAddRollbackPreservesStorageOnStopFailure(t *testing.T) {
	m := NewOSDManager(nil)
	m.fs = afero.NewMemMapFs()
	path := getOSDDataPath(7)
	require.NoError(t, m.fs.MkdirAll(path, 0700))
	require.NoError(t, afero.WriteFile(m.fs, filepath.Join(path, "ready"), nil, 0600))
	require.NoError(t, afero.WriteFile(m.fs, filepath.Join(path, "block"), []byte("retain"), 0600))
	deletedRecord := false
	m.state = &mocks.MockState{DBObj: &mocks.MockDB{TxFn: func(context.Context, func(context.Context, *sql.Tx) error) error {
		deletedRecord = true
		return nil
	}}}
	m.runner = lifecycleRunner{run: func(name string, args ...string) (string, error) {
		require.Equal(t, "microceph-pebble", filepath.Base(name), "no device cleanup before verified stop")
		require.Equal(t, []string{"osd-stop", "7"}, args)
		return "", errors.New("cannot verify exit")
	}}
	aux := &generatedAuxDevicesManifest{
		WAL: &generatedAuxDevice{ParentPath: "/dev/carrier", Partition: 1, Encrypted: true},
		DB:  &generatedAuxDevice{ParentPath: "/dev/carrier", Partition: 2, Encrypted: true},
	}
	err := m.rollbackAddOSD(context.Background(), &types.DiskParameter{Path: "/dev/test"}, 7, aux, true)
	require.ErrorContains(t, err, "cannot verify exit")
	exists, err := afero.Exists(m.fs, filepath.Join(path, "block"))
	require.NoError(t, err)
	require.True(t, exists, "rollback must retain storage when stop is unverified")
	require.False(t, deletedRecord, "retain the recovery record")
}
