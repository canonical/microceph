package ceph

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/canonical/microceph/microceph/common"
)

func TestSMBNativeLaunchGatesAccountsAndExecWithinTheApp(t *testing.T) {
	cases := []struct {
		failure string
		want    []string
	}{
		{"namespace", []string{"namespace"}},
		{"ctdb", []string{"namespace", "ctdb"}},
		{"cancel", []string{"namespace", "ctdb"}},
		{"accounts", []string{"namespace", "ctdb", "accounts"}},
		{"stop", []string{"namespace", "ctdb", "accounts"}},
		{"", []string{"namespace", "ctdb", "accounts", "exec"}},
	}
	for _, tc := range cases {
		name := tc.failure
		if name == "" {
			name = "success"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "alpha")
			require.NoError(t, smbAtomicWrite(filepath.Join(base, "owner.json"), []byte("alpha")))
			require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), []byte(`{"clustered":true,"ctdb_pnn":2}`)))
			t.Setenv("SNAP", "/snap")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			events := []string{}
			ops := smbRuntimeOps{
				enter: func(plan smbRolePlan) error {
					events = append(events, "namespace")
					if tc.failure == "namespace" {
						return errors.New("namespace refused")
					}
					return nil
				},
				ctdbReady: func(context.Context, string) error {
					events = append(events, "ctdb")
					if tc.failure == "ctdb" {
						return errors.New("CTDB not ready")
					}
					if tc.failure == "cancel" {
						cancel()
					}
					return nil
				},
				accounts: func(context.Context, string) error {
					events = append(events, "accounts")
					if tc.failure == "accounts" {
						return errors.New("SAM refused")
					}
					if tc.failure == "stop" {
						return smbAtomicWrite(filepath.Join(root, "stop-requested"), []byte("stop"))
					}
					return nil
				},
				exec: func(plan smbRolePlan) error {
					events = append(events, "exec")
					require.Equal(t, "/snap/bin/smbd", plan.Args[0])
					return nil
				},
			}
			err := launchSMBNative(ctx, root, "smbd", ops)
			if tc.failure == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, tc.want, events)
		})
	}
}

func TestSMBNativeRolePlanKeepsFixedCommandsAndPrivateMounts(t *testing.T) {
	root := "/native/alpha"
	plan, err := smbNativeRolePlan("smbd", root, smbLaunch{Clustered: true, CustomDNS: true}, "/snap/microceph/x1")
	require.NoError(t, err)
	require.Equal(t, []string{"/snap/microceph/x1/bin/smbd", "--foreground", "--no-process-group", "--configfile", root + "/config/smb.conf"}, plan.Args)
	require.Equal(t, smbNativeMount{Target: "/", Flags: unix.MS_REC | unix.MS_SLAVE}, plan.Mounts[0])
	for _, mount := range []smbNativeMount{
		{Source: root + "/config/passwd", Target: "/etc/passwd", Flags: unix.MS_BIND},
		{Source: root + "/ctdb/run", Target: "/run/ctdb", Flags: unix.MS_BIND},
		{Source: root + "/config/resolv.conf", Target: "/etc/resolv.conf", Flags: unix.MS_BIND},
	} {
		require.Contains(t, plan.Mounts, mount)
	}
	require.NotContains(t, plan.Args, "snap")
	require.Equal(t, root+"/ctdb", plan.Env["CTDB_BASE"])
	metrics, err := smbNativeRolePlan("smbmetrics", root, smbLaunch{MetricsPort: 19922}, "/snap")
	require.NoError(t, err)
	require.Equal(t, []string{"/snap/bin/smbmetrics", "--address", "0.0.0.0", "--port", "19922"}, metrics.Args)
	require.Equal(t, "/snap/bin/testparm", metrics.Env["SMBMETRICS_TESTPARM"])
	require.Equal(t, "/snap/bin/ctdb", metrics.Env["SMBMETRICS_CTDB"])
	for _, role := range []string{"ctdbd", "ctdb-nodes", "ctdb-control", "/bin/sh"} {
		_, err = smbNativeRolePlan(role, root, smbLaunch{}, "/snap")
		require.Error(t, err)
	}
}

func TestSMBNativeMountpointRefusesForeignOrNonemptyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-ctdb")
	owner := uint32(os.Geteuid())
	require.NoError(t, prepareSMBCTDBMountpoint(path, owner))
	require.NoError(t, prepareSMBCTDBMountpoint(path, owner))
	require.Error(t, prepareSMBCTDBMountpoint(path, owner+1))
	require.NoError(t, os.Chmod(path, 0777))
	require.Error(t, prepareSMBCTDBMountpoint(path, owner))
	require.NoError(t, os.Chmod(path, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(path, "unrelated"), []byte("retain"), 0600))
	require.Error(t, prepareSMBCTDBMountpoint(path, owner))
	data, err := os.ReadFile(filepath.Join(path, "unrelated"))
	require.NoError(t, err)
	require.Equal(t, "retain", string(data))
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(path, link))
	require.Error(t, prepareSMBCTDBMountpoint(link, owner))
}

func TestSMBNativeCTDBPreparationUsesOnlyPinnedMinimalEvents(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "alpha")
	snap := filepath.Join(base, "snap")
	require.NoError(t, smbPrivateDir(filepath.Join(root, "ctdb/run")))
	for _, name := range []string{"ctdb-eventd", "ctdb_mutex_ceph_rados_helper", "ctdb_recovery_helper", "ctdb_takeover_helper", "ctdb_lock_helper"} {
		file := filepath.Join(snap, "libexec/ctdb", name)
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
		require.NoError(t, os.WriteFile(file, []byte("helper"), 0700))
	}
	for _, name := range []string{"functions", "notify.sh", "events/legacy/00.ctdb.script"} {
		file := filepath.Join(snap, "share/ctdb", name)
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
		require.NoError(t, os.WriteFile(file, []byte("resource"), 0700))
	}
	t.Setenv("SNAP", snap)
	require.NoError(t, os.WriteFile(filepath.Join(root, "ctdb/run/ctdb-nodes.pid"), []byte("stale"), 0600))
	require.NoError(t, prepareSMBCTDBFiles(context.Background(), root, snap))
	target, err := os.Readlink(filepath.Join(root, "ctdb/events/legacy/00.ctdb.script"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(snap, "share/ctdb/events/legacy/00.ctdb.script"), target)
	_, err = os.Stat(filepath.Join(root, "ctdb/run/ctdb-nodes.pid"))
	require.True(t, os.IsNotExist(err))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ctdb/events/legacy/10.interface.script"), []byte("must not delete"), 0600))
	require.Error(t, prepareSMBCTDBFiles(context.Background(), root, snap))
	require.FileExists(t, filepath.Join(root, "ctdb/events/legacy/10.interface.script"))
}

func TestSMBGoReadinessUsesActualPIDControlAndMetricsEndpoint(t *testing.T) {
	useSMBNativeProcesses(t)
	root := filepath.Join(t.TempDir(), "alpha")
	snap := t.TempDir()
	require.NoError(t, smbPrivateDir(filepath.Join(root, "samba/run")))
	require.NoError(t, os.MkdirAll(filepath.Join(snap, "bin"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/smbcontrol"), []byte("#!/bin/sh\ntest \"$1\" = --configfile && test \"$4\" = ping\n"), 0700))
	t.Setenv("SNAP", snap)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/metrics", r.URL.Path)
		_, _ = w.Write([]byte("metric 1\n"))
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), []byte(`{"metrics_port":`+strconv.Itoa(port)+`}`)))
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "samba/run/smbd.pid"), []byte(strconv.Itoa(os.Getpid()))))
	require.NoError(t, smbReadyNative(context.Background(), root, "smbd"))
	require.NoError(t, smbReadyNative(context.Background(), root, "smbmetrics"))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/smbcontrol"), []byte("#!/bin/sh\necho private-control-error >&2\nexit 1\n"), 0700))
	err := smbReadyNative(context.Background(), root, "smbd")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-control-error")
	diagnostic, readErr := os.ReadFile(filepath.Join(root, "diagnostics/native-error"))
	require.NoError(t, readErr)
	require.Contains(t, string(diagnostic), "private-control-error")
	require.NoError(t, os.Remove(filepath.Join(root, "samba/run/smbd.pid")))
	require.Error(t, smbReadyNative(context.Background(), root, "smbd"))
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "stop-requested"), []byte("stop")))
	require.Error(t, smbReadyNative(context.Background(), root, "smbmetrics"))
}

func TestSMBGoReadinessRequiresLiveMonitorAndExactOwner(t *testing.T) {
	base, err := os.MkdirTemp("", "smb-go-ready-")
	require.NoError(t, err)
	defer os.RemoveAll(base)
	root := filepath.Join(base, "alpha")
	require.NoError(t, smbPrivateDir(filepath.Join(root, "ctdb/run")))
	require.NoError(t, smbAtomicWrite(filepath.Join(base, "owner.json"), []byte("alpha")))
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), []byte(`{"clustered":true,"ctdb_pnn":2}`)))
	old := common.ProcessExec
	recorder := &smbNativeCommandRecorder{}
	common.ProcessExec = recorder
	defer func() { common.ProcessExec = old }()
	listener, err := net.Listen("unix", filepath.Join(root, "ctdb/run/native-ready.socket"))
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err == nil {
			_, _ = conn.Write([]byte("1"))
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	require.NoError(t, smbReadyNative(context.Background(), root, "ctdbd"))
	require.Empty(t, recorder.name, "daemon readiness must not execute CTDB or re-enter snap run")
	require.NoError(t, smbAtomicWrite(filepath.Join(base, "owner.json"), []byte("other")))
	require.Error(t, smbReadyNative(context.Background(), root, "ctdbd"))
}
