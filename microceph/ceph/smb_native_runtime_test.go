package ceph

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func localNativeFixture(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "alpha")
	snap := t.TempDir()
	require.NoError(t, smbAtomicWrite(filepath.Join(filepath.Dir(root), "owner.json"), []byte("alpha")))
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), []byte(`{"startup":{"smbd":{"prepared":true}}}`)))
	for name, data := range map[string]string{"identity/local-sam": smbLocalSAMFence, "samba/private/passdb.tdb": "retained-native-identity", "samba/private/secrets.tdb": "retained-native-identity"} {
		require.NoError(t, smbAtomicWrite(filepath.Join(root, name), []byte(data)))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(snap, "bin"), 0700))
	t.Setenv("SNAP", snap)
	return root, snap
}

func TestSMBPrivateClientNeverExecutesServersOrMutatesParent(t *testing.T) {
	root, snap := localNativeFixture(t)
	before, err := os.Readlink("/proc/self/ns/mnt")
	require.NoError(t, err)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	for _, name := range []string{"smbd", "winbindd", "ctdbd", "microcephd", "sh", "ctdb", "wbinfo", "getent"} {
		_, err := newSMBPrivateCommand(context.Background(), root, []string{filepath.Join(snap, "bin", name)})
		require.Error(t, err)
	}
	argv := []string{filepath.Join(snap, "bin/python3"), "-B", filepath.Join(snap, "smb/native.py"), "accounts", filepath.Join(root, "config/.native-input-test")}
	cmd, err := newSMBPrivateCommand(context.Background(), root, argv)
	require.NoError(t, err)
	require.Equal(t, uintptr(syscall.CLONE_NEWNS), cmd.SysProcAttr.Cloneflags)
	require.Zero(t, cmd.SysProcAttr.Unshareflags)
	require.True(t, cmd.SysProcAttr.Setpgid)
	require.Zero(t, cmd.SysProcAttr.Pdeathsig)
	require.Contains(t, cmd.Args[2], "smb_setup_nss")
	require.Contains(t, cmd.Env, "SMB_EXPECTED_ROOT="+root)
	after, err := os.Readlink("/proc/self/ns/mnt")
	require.NoError(t, err)
	require.Equal(t, before, after)
	afterCWD, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, cwd, afterCWD)
	require.Empty(t, os.Getenv("SMB_EXPECTED_ROOT"))
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "stop-requested"), []byte("stop")))
	_, err = newSMBPrivateCommand(context.Background(), root, argv)
	require.Error(t, err)
}

func TestSMBGoReadinessUsesActualPIDControl(t *testing.T) {
	useSMBNativeProcesses(t)
	root, snap := localNativeFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/smbcontrol"), []byte("#!/bin/sh\ntest \"$1\" = --configfile && test \"$4\" = ping\n"), 0700))
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "samba/run/smbd.pid"), []byte(strconv.Itoa(os.Getpid()))))
	require.NoError(t, smbReadyNative(context.Background(), root, "smbd"))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/smbcontrol"), []byte("#!/bin/sh\necho private-control-error >&2\nexit 1\n"), 0700))
	err := smbReadyNative(context.Background(), root, "smbd")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-control-error")
	diagnostic, readErr := os.ReadFile(filepath.Join(root, "diagnostics/native-error"))
	require.NoError(t, readErr)
	require.Contains(t, string(diagnostic), "private-control-error")
	require.NoError(t, os.Remove(filepath.Join(root, "samba/run/smbd.pid")))
	require.Error(t, smbReadyNative(context.Background(), root, "smbd"))
}

func TestSMBPrivateCommandFailureStaysInPrivateDiagnostics(t *testing.T) {
	useSMBNativeProcesses(t)
	root, _ := localNativeFixture(t)
	_, err := runSMBPrivateCommand(context.Background(), root, []string{"/bin/sh", "-c", "echo source-password-private >&2; exit 1"}, nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "source-password-private")
	data, err := os.ReadFile(filepath.Join(root, "diagnostics/native-process-error"))
	require.NoError(t, err)
	require.Contains(t, string(data), "source-password-private")
	info, err := os.Stat(filepath.Join(root, "diagnostics/native-process-error"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = runSMBPrivateCommand(ctx, root, []string{"/bin/sh", "-c", "exit 0"}, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSMBDanglingStopIntentRemainsAStop(t *testing.T) {
	common := t.TempDir()
	t.Setenv("SNAP_COMMON", common)
	root := smbRoot("alpha")
	require.NoError(t, smbAtomicWrite(filepath.Join(smbBase(), "owner.json"), []byte("alpha")))
	require.NoError(t, smbPrivateDir(root))
	require.NoError(t, os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "stop-requested")))
	require.Error(t, checkSMBStartIntent(root))
	status, err := SMBLocalStatus(context.Background(), nil)
	require.NoError(t, err)
	require.True(t, status.StopRequested)
	require.False(t, status.Ready)
}

func TestSMBPreparationCancellationCannotIssueServiceStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := false
	ops := smbAppOps{prepare: func(context.Context, string) error { cancel(); return nil }, start: func(context.Context, string) error { started = true; return nil }, ready: func(context.Context, string) error { return nil }, stop: func(string) error { return nil }}
	require.Error(t, startSMBApps(ctx, t.TempDir(), []string{"smbd"}, ops))
	require.False(t, started)
}
