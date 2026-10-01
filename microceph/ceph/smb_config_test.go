package ceph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaterializeSMBConfigWritesCurrentSourcesAndRemovesStaleUsers(t *testing.T) {
	_, conf, runtime := smbTestPaths(t, false)
	const userURI = "rados:mon-config-key:smb/config/files/users-groups.0.json"
	preserveSMBTestGlobal(t, &fetchSMBSourceFunc)
	fetchSMBSourceFunc = func(_ context.Context, uri string) ([]byte, error) {
		sources := map[string]string{
			"rados://.smb/files/config.smb": smbTestContainer,
			userURI:                         `{"samba-container-config":"v0","users":{"all_entries":[]}}`,
		}
		data, ok := sources[uri]
		if !ok {
			return nil, fmt.Errorf("unexpected source %q", uri)
		}
		return []byte(data), nil
	}
	stale := filepath.Join(runtime, "users-1.json")
	smbTestWrite(t, stale, "stale")
	placement := &SMBServicePlacement{ClusterID: "files", ConfigURI: "rados://.smb/files/config.smb", UserSources: []string{userURI}}
	require.NoError(t, materializeSMBConfig(context.Background(), placement))
	assert.FileExists(t, filepath.Join(runtime, "container.json"))
	assert.FileExists(t, filepath.Join(runtime, "users-0.json"))
	assert.NoFileExists(t, stale)
	assert.Equal(t, "files\n", readSMBConfigFile(t, filepath.Join(runtime, "cluster-id")))
	assert.Equal(t, smbBaseConfig, readSMBConfigFile(t, filepath.Join(conf, "samba", "smb.conf")))
	// Reapplying after setting and then clearing a custom port must not retain it.
	for _, port := range []int{1445, 0} {
		placement.CustomPorts = nil
		expected := smbBaseConfig
		if port != 0 {
			placement.CustomPorts = map[string]int{"smb": port}
			expected += fmt.Sprintf("smb ports = %d\n", port)
		}
		require.NoError(t, materializeSMBConfig(context.Background(), placement))
		assert.Equal(t, expected, readSMBConfigFile(t, filepath.Join(conf, "samba", "smb.conf")))
	}
}

func TestMaterializeSMBConfigRejectsSourcesBeforeWriting(t *testing.T) {
	for _, tc := range []struct{ name, message string }{
		{"container unavailable", "failed to fetch SMB container configuration"},
		{"user unavailable", "failed to fetch SMB user configuration"},
		{"non-direct VFS", "direct samba-vfs/new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, runtime := smbTestPaths(t, false)
			preserveSMBTestGlobal(t, &fetchSMBSourceFunc)
			fetchSMBSourceFunc = func(_ context.Context, uri string) ([]byte, error) {
				if tc.name == "user unavailable" && uri == "rados://.smb/files/config.smb" {
					return []byte(smbTestContainer), nil
				}
				if tc.name == "non-direct VFS" {
					return []byte(`{"shares":{"files":{"options":{"vfs objects":"acl_xattr ceph"}}}}`), nil
				}
				return nil, errors.New("source unavailable")
			}
			placement := &SMBServicePlacement{ClusterID: "files", ConfigURI: "rados://.smb/files/config.smb"}
			if tc.name == "user unavailable" {
				placement.UserSources = []string{"rados:mon-config-key:smb/config/files/users-groups.0.json"}
			}
			require.ErrorContains(t, materializeSMBConfig(context.Background(), placement), tc.message)
			assert.NoDirExists(t, runtime)
		})
	}
}

func TestMaterializeSMBConfigWritesClusteredRuntimeConfiguration(t *testing.T) {
	_, _, runtime := smbTestPaths(t, false)
	t.Setenv("SNAP", "/snap/microceph/current")
	smbTestSource(t, `{"samba-container-config":"v0","configs":{"files":{"instance_features":["ctdb"]}},"shares":{"files":{"options":{"vfs objects":"acl_xattr ceph_new","ceph_new:proxy":"no"}}}}`)
	placement := &SMBServicePlacement{
		ClusterID: "files", ConfigURI: "rados://.smb/files/config.smb", Features: []string{"clustered"},
		ClusterMetaURI: "rados://.smb/files/cluster.meta.json", ClusterLockURI: "rados://.smb/files/cluster.meta.lock",
		BindAddrs: []smbBindAddress{{Network: "10.0.0.0/24"}}, ctdb: &smbCTDBPlacement{Rank: 1, Identity: "smb.files.node-b"},
	}
	require.NoError(t, materializeSMBConfig(context.Background(), placement))
	assert.Equal(t, "1\n", readSMBConfigFile(t, filepath.Join(runtime, "ctdb-rank")))
	assert.Equal(t, "smb.files.node-b\n", readSMBConfigFile(t, filepath.Join(runtime, "ctdb-identity")))
	assert.JSONEq(t, `{"samba-container-config":"v0","ctdb":{
		"recovery_lock":"!/snap/microceph/current/bin/python3 /snap/microceph/current/commands/sambacc.start --samba-command-prefix /snap/microceph/current/commands/samba-command ctdb-rados-mutex rados://.smb/files/cluster.meta.lock",
		"cluster_meta_uri":"rados://.smb/files/cluster.meta.json",
		"nodes_cmd":"/snap/microceph/current/bin/python3 /snap/microceph/current/commands/sambacc.start ctdb-list-nodes",
		"conf_file_includes":["/var/lib/samba/smb.ctdb.conf"]}}`, readSMBConfigFile(t, filepath.Join(runtime, "ctdb.json")))
	placement.ctdb = &smbCTDBPlacement{Rank: 0, Identity: "smb.files.node-a"}
	require.NoError(t, materializeSMBConfig(context.Background(), placement))
	assert.Equal(t, "0\n", readSMBConfigFile(t, filepath.Join(runtime, "ctdb-rank")))
	assert.Equal(t, "smb.files.node-a\n", readSMBConfigFile(t, filepath.Join(runtime, "ctdb-identity")))
}

func TestMaterializeSMBCTDBConfigUsesRevisionIndependentHelpers(t *testing.T) {
	placement := &SMBServicePlacement{
		ClusterID: "files", ClusterMetaURI: "rados://.smb/files/cluster.meta.json", ClusterLockURI: "rados://.smb/files/cluster.meta.lock",
		ctdb: &smbCTDBPlacement{Rank: 0, Identity: "smb.files.node-a"},
	}
	for _, revision := range []string{"x3", "x4", "1234"} {
		t.Run(revision, func(t *testing.T) {
			t.Setenv("SNAP", "/snap/microceph/"+revision)
			dir := t.TempDir()
			require.NoError(t, materializeSMBCTDBConfig(dir, placement))
			config := readSMBConfigFile(t, filepath.Join(dir, "ctdb.json"))
			for _, executable := range []string{"bin/python3", "commands/sambacc.start", "commands/samba-command"} {
				assert.Contains(t, config, "/snap/microceph/current/"+executable)
			}
			assert.NotContains(t, config, "/snap/microceph/"+revision+"/")
		})
	}
}

func TestMaterializeSMBCTDBConfigReportsTheFailedArtifact(t *testing.T) {
	runtime := t.TempDir()
	preserveSMBTestGlobal(t, &writeSMBFileFunc)
	writeSMBFileFunc = func(path string, data []byte, mode os.FileMode) error {
		if filepath.Base(path) == "ctdb-rank.tmp" {
			return errors.New("disk full")
		}
		return os.WriteFile(path, data, mode)
	}
	placement := &SMBServicePlacement{
		ClusterID: "files", ClusterMetaURI: "rados://.smb/files/cluster.meta.json", ClusterLockURI: "rados://.smb/files/cluster.meta.lock",
		ctdb: &smbCTDBPlacement{Rank: 1, Identity: "smb.files.node-b"},
	}
	require.ErrorContains(t, materializeSMBCTDBConfig(runtime, placement), "failed to write CTDB rank")
	assert.NoFileExists(t, filepath.Join(runtime, "ctdb-identity"))
}

func TestWriteSMBCTDBAddressWritesPrivateAddressAndSocketInclude(t *testing.T) {
	root, _, runtime := smbTestPaths(t, true)
	require.NoError(t, os.MkdirAll(runtime, 0700))
	include := filepath.Join(root, "data", "samba", "smb.ctdb.conf")
	const base = "[global]\nctdbd socket = /run/ctdb/ctdbd.socket\nbind interfaces only = yes\ninterfaces = 10.0.0.12\n"
	for _, port := range []int{1445, 0} {
		require.NoError(t, writeSMBCTDBAddress("10.10.10.12", "10.0.0.12", port))
		assert.Equal(t, "10.10.10.12\n", readSMBConfigFile(t, filepath.Join(runtime, "ctdb-address")))
		expected := base
		if port != 0 {
			expected += fmt.Sprintf("smb ports = %d\n", port)
		}
		assert.Equal(t, expected, readSMBConfigFile(t, include))
	}
}

func TestWriteSMBFileAtomicPreservesDestinationOnFailure(t *testing.T) {
	for _, stage := range []string{"temporary write", "rename"} {
		t.Run(stage, func(t *testing.T) {
			preserveSMBTestGlobal(t, &writeSMBFileFunc)
			preserveSMBTestGlobal(t, &renameSMBFileFunc)
			path := filepath.Join(t.TempDir(), "config")
			smbTestWrite(t, path, "original")
			if stage == "temporary write" {
				writeSMBFileFunc = func(string, []byte, os.FileMode) error { return errors.New("write failed") }
			} else {
				renameSMBFileFunc = func(string, string) error { return errors.New("rename failed") }
			}
			require.Error(t, writeSMBFileAtomic(path, []byte("replacement"), 0600))
			assert.Equal(t, "original", readSMBConfigFile(t, path))
			assert.NoFileExists(t, path+".tmp")
		})
	}
}

func readSMBConfigFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
