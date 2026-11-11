package ceph

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSMBDataKeySharelessAndCapOnlyUpdate(t *testing.T) {
	r := withMockRunner(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "data.keyring")
	// No caps are passed: the unchanged mgr owns all CephFS grants.
	r.On("RunCommandContext", ctx, "ceph", "auth", "get-or-create", "client.smb.fs.cluster.alpha", "--format", "json").
		Return(`[{"entity":"client.smb.fs.cluster.alpha","key":"AQAAAA==","caps":{}}]`, nil).Once()
	require.NoError(t, ensureSMBDataKey(ctx, "alpha", path))
	initial, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), initial.Mode().Perm())
	r.On("RunCommandContext", ctx, "ceph", "auth", "get-or-create", "client.smb.fs.cluster.alpha", "--format", "json").
		Return(`[{"entity":"client.smb.fs.cluster.alpha","key":"AQAAAA==","caps":{"mds":"allow rw","mon":"allow r"}}]`, nil).Once()
	require.NoError(t, ensureSMBDataKey(ctx, "alpha", path))
	after, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, initial.ModTime(), after.ModTime(), "caps alone must not rewrite or rotate the gateway key")
	r.On("RunCommandContext", ctx, "ceph", "auth", "get-or-create", "client.smb.fs.cluster.alpha", "--format", "json").
		Return(`[{"entity":"client.smb.fs.cluster.alpha","key":"ROTATED"}]`, nil).Once()
	require.ErrorContains(t, ensureSMBDataKey(ctx, "alpha", path), "explicit recovery")
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(contents), "ROTATED")
	require.NotContains(t, string(contents), "caps")
}

func TestSMBDataKeyRejectsRetainedSymlinks(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "ancestor"}[ancestor], func(t *testing.T) {
			r := withMockRunner(t)
			ctx := context.Background()
			base := t.TempDir()
			outside := filepath.Join(base, "outside")
			require.NoError(t, os.Mkdir(outside, 0700))
			content := []byte("[client.smb.fs.cluster.alpha]\n\tkey = AQAAAA==\n")
			require.NoError(t, os.WriteFile(filepath.Join(outside, "keyring"), content, 0600))
			path := filepath.Join(base, "keyring")
			if ancestor {
				require.NoError(t, os.Symlink(outside, filepath.Join(base, "link")))
				path = filepath.Join(base, "link", "keyring")
			} else {
				require.NoError(t, os.Symlink(filepath.Join(outside, "keyring"), path))
			}
			r.On("RunCommandContext", ctx, "ceph", "auth", "get-or-create", "client.smb.fs.cluster.alpha", "--format", "json").
				Return(`[{"entity":"client.smb.fs.cluster.alpha","key":"AQAAAA=="}]`, nil).Maybe()
			require.Error(t, ensureSMBDataKey(ctx, "alpha", path), "equal key bytes must not bypass private-path guards")
			retained, err := os.ReadFile(filepath.Join(outside, "keyring"))
			require.NoError(t, err)
			require.Equal(t, content, retained)
		})
	}
}

func TestSMBPrivateWritesRejectSymlinksAndCleanFailedRename(t *testing.T) {
	for _, write := range []struct {
		name string
		call func(string, []byte) error
	}{{"native", writeSMBNativeFile}, {"intent", smbAtomicWrite}} {
		t.Run(write.name, func(t *testing.T) {
			base := t.TempDir()
			outside := filepath.Join(base, "outside")
			require.NoError(t, os.Mkdir(outside, 0700))
			require.NoError(t, os.Symlink(outside, filepath.Join(base, "link")))
			require.Error(t, write.call(filepath.Join(base, "link", "secret"), []byte("secret")))
			entries, err := os.ReadDir(outside)
			require.NoError(t, err)
			require.Empty(t, entries)
			path := filepath.Join(base, "private", "keyring")
			require.NoError(t, write.call(path, []byte("retained")))
			info, err := os.Stat(filepath.Dir(path))
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0700), info.Mode().Perm())
			info, err = os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0600), info.Mode().Perm())
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Mkdir(path, 0700))
			require.Error(t, write.call(path, []byte("secret")))
			entries, err = os.ReadDir(filepath.Dir(path))
			require.NoError(t, err)
			require.Len(t, entries, 1, "failed rename must leave only the original directory")
		})
	}
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.WriteFile(outside, []byte("preserve"), 0600))
	path := filepath.Join(base, "keyring")
	require.NoError(t, os.Symlink(outside, path))
	require.Error(t, writeSMBNativeFile(path, []byte("secret")))
	contents, err := os.ReadFile(outside)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(contents))
}

func TestSMBAtomicWriteKeepsInterruptedTemporaryFilePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner")
	require.NoError(t, os.WriteFile(path+".tmp", []byte("interrupted"), 0644))
	require.NoError(t, smbAtomicWrite(path, []byte("private")))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
