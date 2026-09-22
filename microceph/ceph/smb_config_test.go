package ceph

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaterializeSMBConfigWritesCurrentSourcesAndRemovesStaleUsers(t *testing.T) {
	tempDir := t.TempDir()
	confPath := filepath.Join(tempDir, "conf")
	runtimePath := filepath.Join(tempDir, "samba")
	configPath := filepath.Join(runtimePath, "container.json")
	userPath := filepath.Join(runtimePath, "users-0.json")
	staleUserPath := filepath.Join(runtimePath, "users-1.json")

	originalPaths := constants.GetPathConst
	defer func() {
		constants.GetPathConst = originalPaths
	}()
	constants.GetPathConst = func() constants.PathConst {
		return constants.PathConst{ConfPath: confPath}
	}

	originalFetch := fetchSMBSourceFunc
	defer func() {
		fetchSMBSourceFunc = originalFetch
	}()
	fetchSMBSourceFunc = func(uri string) ([]byte, error) {
		sources := map[string]string{
			"rados://.smb/files/config.smb": `{
				"samba-container-config": "v0",
				"shares": {
					"files": {
						"options": {
							"vfs objects": "acl_xattr ceph_snapshots ceph_new",
							"ceph_new:proxy": "no"
						}
					}
				}
			}`,
			"rados:mon-config-key:smb/config/files/users-groups.0.json": `{
				"samba-container-config": "v0",
				"users": {"all_entries": []}
			}`,
		}
		data, ok := sources[uri]
		if !ok {
			return nil, fmt.Errorf("unexpected source %q", uri)
		}
		return []byte(data), nil
	}

	err := os.MkdirAll(runtimePath, 0700)
	require.NoError(t, err)
	err = os.WriteFile(staleUserPath, []byte("stale"), 0600)
	require.NoError(t, err)

	placement := &SMBServicePlacement{
		ClusterID: "files",
		ConfigURI: "rados://.smb/files/config.smb",
		UserSources: []string{
			"rados:mon-config-key:smb/config/files/users-groups.0.json",
		},
	}

	err = materializeSMBConfig(placement)

	require.NoError(t, err)
	assert.FileExists(t, configPath)
	assert.FileExists(t, userPath)
	assert.NoFileExists(t, staleUserPath)
	assert.Equal(t, "files\n", readSMBConfigFile(t, filepath.Join(runtimePath, "cluster-id")))
	assert.Equal(t, "[global]\nconfig backend = registry\nlock directory = /var/lib/samba/lock\npid directory = /var/lib/samba/run\nncalrpc dir = /var/lib/samba/ncalrpc\nwinbindd socket directory = /var/lib/samba/winbindd\nstate directory = /var/lib/samba/state\ncache directory = /var/cache/samba\nprivate dir = /var/lib/samba/private\n", readSMBConfigFile(t, filepath.Join(confPath, "samba", "smb.conf")))
}

func TestMaterializeSMBConfigReportsSourceFailuresBeforeWriting(t *testing.T) {
	tests := []struct {
		name       string
		userSource bool
		expected   string
	}{
		{
			name:     "container source",
			expected: "failed to fetch SMB container configuration",
		},
		{
			name:       "user source",
			userSource: true,
			expected:   "failed to fetch SMB user configuration",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tempDir := t.TempDir()
			confPath := filepath.Join(tempDir, "conf")
			runtimePath := filepath.Join(tempDir, "samba")
			originalPaths := constants.GetPathConst
			defer func() {
				constants.GetPathConst = originalPaths
			}()
			constants.GetPathConst = func() constants.PathConst {
				return constants.PathConst{ConfPath: confPath}
			}

			originalFetch := fetchSMBSourceFunc
			defer func() {
				fetchSMBSourceFunc = originalFetch
			}()
			fetchSMBSourceFunc = func(uri string) ([]byte, error) {
				if test.userSource && uri == "rados://.smb/files/config.smb" {
					return []byte(`{"shares":{"files":{"options":{"vfs objects":"ceph_new","ceph_new:proxy":"no"}}}}`), nil
				}
				return nil, errors.New("source unavailable")
			}

			placement := &SMBServicePlacement{
				ClusterID: "files",
				ConfigURI: "rados://.smb/files/config.smb",
			}
			if test.userSource {
				placement.UserSources = []string{
					"rados:mon-config-key:smb/config/files/users-groups.0.json",
				}
			}

			err := materializeSMBConfig(placement)

			assert.ErrorContains(t, err, test.expected)
			assert.NoDirExists(t, runtimePath)
		})
	}
}

func TestMaterializeSMBConfigWritesClusteredRuntimeConfiguration(t *testing.T) {
	tempDir := t.TempDir()
	confPath := filepath.Join(tempDir, "conf")
	runtimePath := filepath.Join(tempDir, "samba")
	t.Setenv("SNAP", "/snap/microceph/current")

	originalPaths := constants.GetPathConst
	defer func() {
		constants.GetPathConst = originalPaths
	}()
	constants.GetPathConst = func() constants.PathConst {
		return constants.PathConst{ConfPath: confPath}
	}

	originalFetch := fetchSMBSourceFunc
	defer func() {
		fetchSMBSourceFunc = originalFetch
	}()
	fetchSMBSourceFunc = func(_ string) ([]byte, error) {
		return []byte(`{
			"samba-container-config": "v0",
			"configs": {"files": {"instance_features": ["ctdb"]}},
			"shares": {
				"files": {"options": {
					"vfs objects": "acl_xattr ceph_new",
					"ceph_new:proxy": "no"
				}}
			}
		}`), nil
	}

	placement := &SMBServicePlacement{
		ClusterID:      "files",
		ConfigURI:      "rados://.smb/files/config.smb",
		Features:       []string{"clustered"},
		ClusterMetaURI: "rados://.smb/files/cluster.meta.json",
		ClusterLockURI: "rados://.smb/files/cluster.meta.lock",
		BindAddrs:      []smbBindAddress{{Network: "10.0.0.0/24"}},
		ctdb: &smbCTDBPlacement{
			Rank:     1,
			Identity: "smb.files.node-b",
		},
	}

	err := materializeSMBConfig(placement)

	require.NoError(t, err)
	assert.Equal(t, "1\n", readSMBConfigFile(t, filepath.Join(runtimePath, "ctdb-rank")))
	assert.Equal(t, "smb.files.node-b\n", readSMBConfigFile(t, filepath.Join(runtimePath, "ctdb-identity")))
	assert.JSONEq(t, `{
		"samba-container-config": "v0",
		"ctdb": {
			"recovery_lock": "!/snap/microceph/current/bin/python3 /snap/microceph/current/commands/sambacc.start --samba-command-prefix /snap/microceph/current/commands/samba-command ctdb-rados-mutex rados://.smb/files/cluster.meta.lock",
			"cluster_meta_uri": "rados://.smb/files/cluster.meta.json",
			"nodes_cmd": "/snap/microceph/current/bin/python3 /snap/microceph/current/commands/sambacc.start ctdb-list-nodes",
			"conf_file_includes": ["/var/lib/samba/smb.ctdb.conf"]
		}
	}`, readSMBConfigFile(t, filepath.Join(runtimePath, "ctdb.json")))

	placement.ctdb.Rank = 0
	placement.ctdb.Identity = "smb.files.node-a"
	err = materializeSMBConfig(placement)

	require.NoError(t, err)
	assert.Equal(t, "0\n", readSMBConfigFile(t, filepath.Join(runtimePath, "ctdb-rank")))
	assert.Equal(t, "smb.files.node-a\n", readSMBConfigFile(t, filepath.Join(runtimePath, "ctdb-identity")))
}

func TestMaterializeSMBCTDBConfigReportsTheFailedArtifact(t *testing.T) {
	runtimeDir := t.TempDir()
	originalWrite := writeSMBFileFunc
	defer func() {
		writeSMBFileFunc = originalWrite
	}()
	writeSMBFileFunc = func(path string, data []byte, mode os.FileMode) error {
		if filepath.Base(path) == "ctdb-rank.tmp" {
			return errors.New("disk full")
		}
		return os.WriteFile(path, data, mode)
	}
	placement := &SMBServicePlacement{
		ClusterID:      "files",
		ClusterMetaURI: "rados://.smb/files/cluster.meta.json",
		ClusterLockURI: "rados://.smb/files/cluster.meta.lock",
		ctdb: &smbCTDBPlacement{
			Rank:     1,
			Identity: "smb.files.node-b",
		},
	}

	err := materializeSMBCTDBConfig(runtimeDir, placement)

	assert.ErrorContains(t, err, "failed to write CTDB rank")
	assert.NoFileExists(t, filepath.Join(runtimeDir, "ctdb-identity"))
}

func TestWriteSMBCTDBAddressWritesSambaBindInclude(t *testing.T) {
	tempDir := t.TempDir()
	confPath := filepath.Join(tempDir, "conf")
	dataPath := filepath.Join(tempDir, "data")

	originalPaths := constants.GetPathConst
	defer func() {
		constants.GetPathConst = originalPaths
	}()
	constants.GetPathConst = func() constants.PathConst {
		return constants.PathConst{ConfPath: confPath, DataPath: dataPath}
	}
	err := os.MkdirAll(filepath.Join(tempDir, "samba"), 0700)
	require.NoError(t, err)
	placement := &SMBServicePlacement{
		BindAddrs: []smbBindAddress{{Network: "10.0.0.0/24"}},
	}

	err = writeSMBCTDBAddress("10.0.0.12", placement)

	require.NoError(t, err)
	assert.Equal(t, "10.0.0.12\n", readSMBConfigFile(t, filepath.Join(tempDir, "samba", "ctdb-address")))
	assert.Equal(t, "[global]\nctdbd socket = /run/ctdb/ctdbd.socket\nbind interfaces only = yes\ninterfaces = 10.0.0.12\n", readSMBConfigFile(t, filepath.Join(dataPath, "samba", "smb.ctdb.conf")))
}

func TestMaterializeSMBConfigRejectsNonDirectContainerBeforeWriting(t *testing.T) {
	tempDir := t.TempDir()
	confPath := filepath.Join(tempDir, "conf")
	runtimePath := filepath.Join(tempDir, "samba")

	originalPaths := constants.GetPathConst
	defer func() {
		constants.GetPathConst = originalPaths
	}()
	constants.GetPathConst = func() constants.PathConst {
		return constants.PathConst{ConfPath: confPath}
	}

	originalFetch := fetchSMBSourceFunc
	defer func() {
		fetchSMBSourceFunc = originalFetch
	}()
	fetchSMBSourceFunc = func(_ string) ([]byte, error) {
		return []byte(`{
			"shares": {
				"files": {
					"options": {"vfs objects": "acl_xattr ceph"}
				}
			}
		}`), nil
	}

	placement := &SMBServicePlacement{
		ClusterID: "files",
		ConfigURI: "rados://.smb/files/config.smb",
	}

	err := materializeSMBConfig(placement)

	assert.ErrorContains(t, err, "direct samba-vfs/new")
	assert.NoFileExists(t, filepath.Join(runtimePath, "container.json"))
}

func TestWriteSMBFileAtomicPreservesDestinationOnFailure(t *testing.T) {
	tests := []struct {
		name      string
		configure func()
	}{
		{
			name: "temporary write",
			configure: func() {
				writeSMBFileFunc = func(_ string, _ []byte, _ os.FileMode) error {
					return errors.New("write failed")
				}
			},
		},
		{
			name: "rename",
			configure: func() {
				renameSMBFileFunc = func(_, _ string) error {
					return errors.New("rename failed")
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalWrite := writeSMBFileFunc
			originalRename := renameSMBFileFunc
			defer func() {
				writeSMBFileFunc = originalWrite
				renameSMBFileFunc = originalRename
			}()

			destPath := filepath.Join(t.TempDir(), "config")
			err := os.WriteFile(destPath, []byte("original"), 0600)
			require.NoError(t, err)
			test.configure()

			err = writeSMBFileAtomic(destPath, []byte("replacement"), 0600)

			require.Error(t, err)
			assert.Equal(t, "original", readSMBConfigFile(t, destPath))
			assert.NoFileExists(t, destPath+".tmp")
		})
	}
}

func readSMBConfigFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
