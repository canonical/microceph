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

func TestSMBSourceURIBoundary(t *testing.T) {
	for _, uri := range []string{"rados://.smb/alpha/config.smb", "rados:mon-config-key:smb/config/alpha/users-groups.0.json"} {
		require.NoError(t, validateSMBSourceURI("alpha", uri))
	}
	for _, uri := range []string{"rados://.smb/beta/config.smb", "rados://other/alpha/config.smb", "file:///etc/passwd", "rados://.smb/alpha/../beta", "rados:mon-config-key:smb/config/alphabeta/join.0.json"} {
		require.Error(t, validateSMBSourceURI("alpha", uri), uri)
	}
}
