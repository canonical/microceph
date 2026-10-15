package ceph

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
)

// smbConfinedReadinessRunner rejects the exact invocation independently denied
// in the installed daemon profile. It does not translate snap run into Python
// or simulate AppArmor enforcement; direct helper calls execute real processes.
type smbConfinedReadinessRunner struct {
	t *testing.T
}

func (r smbConfinedReadinessRunner) RunCommand(name string, args ...string) (string, error) {
	return r.RunCommandContext(context.Background(), name, args...)
}

func (r smbConfinedReadinessRunner) RunCommandContext(ctx context.Context, name string, args ...string) (string, error) {
	if name == "snap" {
		require.Equal(r.t, []string{"run", "microceph.ctdb", "ready", "ctdbd"}, args)
		return "", errors.New("installed daemon profile denies execution of snapd")
	}
	require.Equal(r.t, filepath.Join(os.Getenv("SNAP"), "bin/python3"), name)
	return (common.RunnerImpl{}).RunCommandContext(ctx, name, args...)
}

func TestSMBReadinessExecutesDirectHelperWithRealSocket(t *testing.T) {
	python := os.Getenv("MICROCEPH_SMB_TEST_PYTHON")
	if python == "" {
		t.Skip("set MICROCEPH_SMB_TEST_PYTHON and PYTHONPATH for real native helper")
	}
	snap := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(snap, "bin"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(snap, "smb"), 0700))
	require.NoError(t, os.Symlink(python, filepath.Join(snap, "bin/python3")))
	native, err := filepath.Abs("../../snapcraft/smb/native.py")
	require.NoError(t, err)
	require.NoError(t, os.Symlink(native, filepath.Join(snap, "smb/native.py")))
	t.Setenv("SNAP", snap)
	// Unix pathname length is limited; Go's nested test temp path is too long.
	base, err := os.MkdirTemp("", "smb-ready-")
	require.NoError(t, err)
	defer os.RemoveAll(base)
	root := filepath.Join(base, "alpha")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "ctdb/run"), 0700))
	require.NoError(t, smbAtomicWrite(filepath.Join(base, "owner.json"), []byte("alpha")))
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), []byte(`{"clustered":true,"ctdb_pnn":2}`)))
	old := common.ProcessExec
	common.ProcessExec = smbConfinedReadinessRunner{t: t}
	defer func() { common.ProcessExec = old }()
	listener, err := net.Listen("unix", filepath.Join(root, "ctdb/run/native-ready.socket"))
	require.NoError(t, err)
	defer listener.Close()
	// A readiness-only peer, not a CTDB daemon substitute. Python integration
	// tests separately require the peer to run fresh real CLI invocations.
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			_, _ = conn.Write([]byte("1"))
			_ = conn.Close()
		}
	}()
	err = smbNativeCommand(context.Background(), "ready", root, "ctdbd")
	_ = listener.Close()
	<-done
	require.NoError(t, err, "readiness must not re-enter snap run from the daemon")
	// The same direct path serves every caller, including refresh health-only
	// retries. A stale path, EOF or cancellation must never confirm readiness.
	require.Error(t, smbNativeCommand(context.Background(), "ready", root, "ctdb-nodes"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	listener, err = net.Listen("unix", filepath.Join(root, "ctdb/run/native-ready.socket"))
	require.NoError(t, err)
	defer listener.Close()
	started := time.Now()
	require.ErrorIs(t, smbSnapOps(root).ready(ctx, "ctdbd"), context.DeadlineExceeded)
	require.Less(t, time.Since(started), 3*time.Second)
}

func TestSMBClusteredReadinessStartsMonitorBeforeWaitingAndGatesConsumers(t *testing.T) {
	for _, failure := range []string{"", "ctdbd-start", "ctdb-nodes-start", "ctdbd-ready", "ctdb-nodes-ready"} {
		t.Run(failure, func(t *testing.T) {
			events := []string{}
			started := map[string]bool{}
			ops := smbAppOps{
				start: func(role string) error {
					events = append(events, "start "+role)
					started[role] = true // control failure may still start the app
					if failure == role+"-start" {
						return errors.New("uncertain start")
					}
					return nil
				},
				ready: func(ctx context.Context, role string) error {
					events = append(events, "ready "+role)
					if role == "ctdbd" || role == "ctdb-nodes" {
						require.True(t, started["ctdbd"] && started["ctdb-nodes"], "the confined node monitor performs fresh CTDB readiness")
					}
					if failure == role+"-ready" {
						return errors.New("unhealthy CTDB")
					}
					return nil
				},
				stop: func(role string) error { events = append(events, "stop "+role); return nil },
			}
			err := startSMBApps(context.Background(), t.TempDir(), []string{"ctdbd", "ctdb-nodes", "smbd"}, ops)
			if failure == "" {
				require.NoError(t, err)
				require.Equal(t, []string{"start ctdbd", "start ctdb-nodes", "ready ctdbd", "ready ctdb-nodes", "start smbd", "ready smbd"}, events)
			} else {
				require.Error(t, err)
				require.NotContains(t, events, "start smbd", "unconfirmed CTDB must gate Samba/SAM")
				for _, role := range smbStopOrder {
					require.Contains(t, events, "stop "+role, "rollback stops all attempted roles")
				}
			}
		})
	}
}
