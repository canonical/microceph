package ceph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/mock"
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

// The installed CTDB RADOS mutex helper needs read/write object-class access:
// the scratch probe fails with rw and acquires/renews/releases with bounded rwx.
func TestSMBRecoveryKeyCreateAndUpgrade(t *testing.T) {
	for _, state := range []string{"new", "old-caps", "current-caps"} {
		t.Run(state, func(t *testing.T) {
			r := withMockRunner(t)
			ctx := context.Background()
			root := t.TempDir()
			path := filepath.Join(root, "identity", "recovery.keyring")
			want := "[client.smb.cluster.alpha]\n\tkey = AQAAAA==\n"
			if state != "new" {
				require.NoError(t, os.Mkdir(filepath.Dir(path), 0700))
				require.NoError(t, os.WriteFile(path, []byte(want), 0600))
			}
			caps := `{}`
			if state == "old-caps" {
				caps = `{"mon":"allow r","osd":"allow rw pool=.smb namespace=alpha"}`
			}
			if state == "current-caps" {
				caps = `{"mon":"allow r","osd":"allow rwx pool=.smb namespace=alpha"}`
			}
			for repeat := 0; repeat < 2; repeat++ {
				// Supplying new caps to get-or-create rejects an old-cap identity.
				// Read/create without caps, then set only this recovery identity's caps.
				read := r.On("RunCommandContext", ctx, "ceph", "auth", "get-or-create", "client.smb.cluster.alpha", "--format", "json").
					Return(fmt.Sprintf(`[{"entity":"client.smb.cluster.alpha","key":"AQAAAA==","caps":%s}]`, caps), nil).Once()
				r.On("RunCommandContext", ctx, "ceph", "auth", "caps", "client.smb.cluster.alpha",
					"mon", "allow r", "osd", "allow rwx pool=.smb namespace=alpha").
					Return("updated caps", nil).Once().NotBefore(read)
				require.NoError(t, ensureSMBRecoveryKey(ctx, "alpha", root))
				content, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, want, string(content), "cap reconciliation must preserve the shared secret")
				info, err := os.Stat(path)
				require.NoError(t, err)
				require.Equal(t, os.FileMode(0600), info.Mode().Perm())
				caps = `{"mon":"allow r","osd":"allow rwx pool=.smb namespace=alpha"}`
			}
		})
	}
}

func TestSMBRecoveryKeyConcurrentCreate(t *testing.T) {
	r := withMockRunner(t)
	ctx := context.Background()
	root := t.TempDir()
	// Ceph returns an empty success when another caller's create is pending.
	pending := r.On("RunCommandContext", ctx, "ceph", "auth", "get-or-create", "client.smb.cluster.B-2", "--format", "json").
		Return("\n", nil).Once()
	read := r.On("RunCommandContext", ctx, "ceph", "auth", "get-or-create", "client.smb.cluster.B-2", "--format", "json").
		Return(`[{"entity":"client.smb.cluster.B-2","key":"SHARED"}]`, nil).Once().NotBefore(pending)
	r.On("RunCommandContext", ctx, "ceph", "auth", "caps", "client.smb.cluster.B-2",
		"mon", "allow r", "osd", "allow rwx pool=.smb namespace=B-2").
		Return("", nil).Once().NotBefore(read)
	require.NoError(t, ensureSMBRecoveryKey(ctx, "B-2", root))
	content, err := os.ReadFile(filepath.Join(root, "identity", "recovery.keyring"))
	require.NoError(t, err)
	require.Equal(t, "[client.smb.cluster.B-2]\n\tkey = SHARED\n", string(content))
}

func TestSMBRecoveryKeyFailurePreservesKeyring(t *testing.T) {
	for _, failure := range []string{"auth", "read", "caps"} {
		t.Run(failure, func(t *testing.T) {
			r := withMockRunner(t)
			ctx := context.Background()
			root := t.TempDir()
			path := filepath.Join(root, "identity", "recovery.keyring")
			require.NoError(t, os.Mkdir(filepath.Dir(path), 0700))
			before := []byte("retained recovery keyring\n")
			require.NoError(t, os.WriteFile(path, before, 0600))
			output := `[{"entity":"client.smb.cluster.alpha","key":"AQAAAA=="}]`
			var authErr error
			if failure == "auth" {
				authErr = errors.New("private command diagnostic AQAAAA==")
			}
			if failure == "read" {
				output = "unparseable private diagnostic AQAAAA=="
			}
			read := r.On("RunCommandContext", ctx, "ceph", "auth", "get-or-create", "client.smb.cluster.alpha", "--format", "json").
				Return(output, authErr).Once()
			wantErr := "retrieve SMB recovery identity failed"
			if failure == "caps" {
				r.On("RunCommandContext", ctx, "ceph", "auth", "caps", "client.smb.cluster.alpha",
					"mon", "allow r", "osd", "allow rwx pool=.smb namespace=alpha").
					Run(func(_ mock.Arguments) {
						content, err := os.ReadFile(path)
						require.NoError(t, err)
						require.Equal(t, before, content, "do not publish a keyring before caps succeed")
					}).Return("", errors.New("private cap diagnostic AQAAAA==")).Once().NotBefore(read)
				wantErr = "set SMB recovery identity capabilities failed"
			}
			err := ensureSMBRecoveryKey(ctx, "alpha", root)
			require.EqualError(t, err, wantErr)
			content, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, content)
			_, err = os.Stat(path + ".tmp")
			require.True(t, os.IsNotExist(err))
		})
	}
}

func TestSMBRecoveryKeyRejectsInvalidCluster(t *testing.T) {
	withMockRunner(t) // Invalid input must not execute an auth command.
	require.ErrorContains(t, ensureSMBRecoveryKey(context.Background(), "alpha namespace=*", t.TempDir()), "invalid SMB cluster ID")
}
