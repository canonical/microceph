package ceph

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestSMBPrivateClientNeverExecutesServersOrMutatesParent(t *testing.T) {
	root, snap := nativeCTDBFixture(t)
	before, err := os.Readlink("/proc/self/ns/mnt")
	require.NoError(t, err)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	for _, name := range []string{"smbd", "winbindd", "ctdbd", "microcephd", "sh"} {
		_, err := newSMBPrivateCommand(context.Background(), root, []string{filepath.Join(snap, "bin", name)})
		require.Error(t, err)
	}
	cmd, err := newSMBPrivateCommand(context.Background(), root, []string{filepath.Join(snap, "bin/ctdb"), "status"})
	require.NoError(t, err)
	require.Equal(t, uintptr(syscall.CLONE_NEWNS), cmd.SysProcAttr.Cloneflags)
	require.Zero(t, cmd.SysProcAttr.Unshareflags)
	require.True(t, cmd.SysProcAttr.Setpgid)
	require.Zero(t, cmd.SysProcAttr.Pdeathsig)
	require.Contains(t, cmd.Args[2], "smb_setup_ctdb")
	require.NotContains(t, cmd.Args[2], "smb_setup_nss")
	require.Contains(t, cmd.Env, "SMB_CREATE_MOUNTPOINTS=0")
	require.Contains(t, cmd.Env, "SMB_EXPECTED_ROOT="+root)
	after, err := os.Readlink("/proc/self/ns/mnt")
	require.NoError(t, err)
	require.Equal(t, before, after)
	afterCWD, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, cwd, afterCWD)
	require.Empty(t, os.Getenv("SMB_EXPECTED_ROOT"))
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "stop-requested"), []byte("stop")))
	_, err = newSMBPrivateCommand(context.Background(), root, []string{filepath.Join(snap, "bin/ctdb"), "status"})
	require.Error(t, err)
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
	require.Equal(t, "/usr/share/ctdb/events/legacy/00.ctdb.script", target)
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

// Socket connectivity using compiled NSS only, not AD authentication. All /run
// changes are inside a disposable user/mount namespace, never on the host.
func TestSMBADNSSPrivateSocketInAppNamespace(t *testing.T) {
	library := os.Getenv("MICROCEPH_SMB_TEST_NSS_LIBRARY")
	if library == "" {
		t.Skip("set MICROCEPH_SMB_TEST_NSS_LIBRARY to the pinned extracted NSS library")
	}
	if os.Getenv("MICROCEPH_SMB_NSS_CHILD") == "1" {
		runtime.LockOSThread()
		require.NoError(t, unix.Mount("tmpfs", "/run", "tmpfs", 0, "mode=0755"))
		code := `import ctypes,os
class Passwd(ctypes.Structure):
    _fields_=[("name",ctypes.c_void_p),("passwd",ctypes.c_void_p),("uid",ctypes.c_uint),("gid",ctypes.c_uint),("gecos",ctypes.c_void_p),("dir",ctypes.c_void_p),("shell",ctypes.c_void_p)]
lib=ctypes.CDLL(os.environ["MICROCEPH_SMB_TEST_NSS_LIBRARY"])
f=lib._nss_winbind_getpwnam_r
f.argtypes=[ctypes.c_char_p,ctypes.POINTER(Passwd),ctypes.c_void_p,ctypes.c_size_t,ctypes.POINTER(ctypes.c_int)]
buf=ctypes.create_string_buffer(16384);pw=Passwd();err=ctypes.c_int()
f(b"PROBE\\socket-only",ctypes.byref(pw),buf,len(buf),ctypes.byref(err))`
		body := `. "$MICROCEPH_SMB_STARTUP_LIBRARY"; SMB_ROOT="$MICROCEPH_SMB_NSS_ROOT"; SMB_CLUSTERED=1; SMB_WORKGROUP=EXAMPLE; SMB_CREATE_MOUNTPOINTS=1; smb_setup_winbind; exec "$MICROCEPH_SMB_TEST_PYTHON" -B -c "$1"`
		cmd := exec.Command("/bin/bash", "-euc", body, "nss-probe", code)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		require.NoError(t, cmd.Run())
		return
	}
	require.NotEmpty(t, os.Getenv("MICROCEPH_SMB_TEST_PYTHON"))
	root := filepath.Join(t.TempDir(), "alpha")
	require.NoError(t, prepareSMBADPaths(root))
	snap := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(snap, "bin"), 0700))
	for name, program := range map[string]string{"python3": "python3", "mount": "mount"} {
		path, err := exec.LookPath(program)
		require.NoError(t, err)
		require.NoError(t, os.Symlink(path, filepath.Join(snap, "bin", name)))
	}
	t.Setenv("SNAP", snap)
	startup, err := filepath.Abs("../../snapcraft/commands/smb-startup")
	require.NoError(t, err)
	t.Setenv("MICROCEPH_SMB_STARTUP_LIBRARY", startup)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(root, "samba/winbindd/pipe"), Net: "unix"})
	require.NoError(t, err)
	defer listener.Close()
	require.NoError(t, listener.SetDeadline(time.Now().Add(10*time.Second)))
	cmd := exec.Command("unshare", "--user", "--map-root-user", "--mount", "--propagation", "slave", os.Args[0], "-test.run=^TestSMBADNSSPrivateSocketInAppNamespace$")
	cmd.Env = append(os.Environ(), "MICROCEPH_SMB_NSS_CHILD=1", "MICROCEPH_SMB_NSS_ROOT="+root)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	require.NoError(t, cmd.Start())
	conn, acceptErr := listener.AcceptUnix()
	if conn != nil {
		require.NoError(t, conn.Close())
	}
	waitErr := cmd.Wait()
	require.NoError(t, waitErr, output.String())
	require.NoError(t, acceptErr, "compiled NSS did not reach the app-private public socket: %s", output.String())
}
