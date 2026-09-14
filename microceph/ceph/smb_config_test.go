package ceph

import (
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

func readSMBConfigFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
