package ceph

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetServicePlacementTableIncludesSMB(t *testing.T) {
	placement, ok := GetServicePlacementTable()["smb"]

	assert.True(t, ok)
	assert.IsType(t, &SMBServicePlacement{}, placement)
}

func TestSMBServicePlacementPopulateParamsAcceptsDirectVFS(t *testing.T) {
	placement := &SMBServicePlacement{}
	payload := `{
		"cluster_id": "files",
		"config_uri": "rados://.smb/files/config.smb",
		"user_sources": [
			"rados:mon-config-key:smb/config/files/users-groups.0.json"
		]
	}`

	err := placement.PopulateParams(nil, payload)

	assert.NoError(t, err)
	assert.Equal(t, "files", placement.ClusterID)
	assert.Equal(t, "rados://.smb/files/config.smb", placement.ConfigURI)
	assert.Equal(t, []string{"rados:mon-config-key:smb/config/files/users-groups.0.json"}, placement.UserSources)
}

func TestSMBServicePlacementPopulateParamsRejectsUnsupportedFeatures(t *testing.T) {
	placement := &SMBServicePlacement{}
	payload := `{
		"cluster_id": "files",
		"config_uri": "rados://.smb/files/config.smb",
		"features": ["cephfs-proxy"]
	}`

	err := placement.PopulateParams(nil, payload)

	assert.ErrorContains(t, err, "does not support SMB feature 'cephfs-proxy'")
}

func TestSMBServicePlacementPopulateParamsRejectsOtherClusterNamespace(t *testing.T) {
	placement := &SMBServicePlacement{}
	payload := `{
		"cluster_id": "files",
		"config_uri": "rados://.smb/other/config.smb"
	}`

	err := placement.PopulateParams(nil, payload)

	assert.ErrorContains(t, err, "must use SMB cluster namespace 'files'")
}

func TestValidateSMBContainerConfigRequiresDirectCephNew(t *testing.T) {
	config := []byte(`{
		"shares": {
			"files": {
				"options": {
					"vfs objects": "acl_xattr ceph_snapshots ceph_new",
					"ceph_new:proxy": "no"
				}
			}
		}
	}`)

	err := validateSMBContainerConfig(config)

	assert.NoError(t, err)
}

func TestSMBServicePlacementHospitalityRequiresIdentitySwitching(t *testing.T) {
	runner := mocks.NewRunner(t)
	runner.On("RunCommand", "snapctl", "is-connected", "smb-identity").Return("", assert.AnError).Once()
	originalRunner := common.ProcessExec
	defer func() {
		common.ProcessExec = originalRunner
	}()
	common.ProcessExec = runner

	placement := &SMBServicePlacement{ClusterID: "files"}
	err := placement.HospitalityCheck(nil)

	assert.ErrorContains(t, err, "requires the smb-identity interface connection")
}

func TestSMBServicePlacementServiceInitMaterializesConfigAndStartsSMBD(t *testing.T) {
	tempDir := t.TempDir()
	confPath := filepath.Join(tempDir, "conf")

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
			"shares": {
				"files": {
					"options": {
						"vfs objects": "acl_xattr ceph_snapshots ceph_new",
						"ceph_new:proxy": "no"
					}
				}
			}
		}`), nil
	}

	runner := mocks.NewRunner(t)
	runner.On("RunCommand", "ceph", "auth", "get", "client.smb.fs.cluster.files").Return("[client.smb.fs.cluster.files]\\nkey = key\\n", nil).Once()
	runner.On("RunCommand", "snapctl", "services", "microceph.smbd").Return("microceph.smbd disabled inactive", nil).Once()
	runner.On("RunCommand", "snapctl", "start", "microceph.smbd", "--enable").Return("ok", nil).Once()
	originalRunner := common.ProcessExec
	defer func() {
		common.ProcessExec = originalRunner
	}()
	common.ProcessExec = runner

	placement := &SMBServicePlacement{
		ClusterID: "files",
		ConfigURI: "rados://.smb/files/config.smb",
	}

	err := placement.ServiceInit(context.Background(), nil)

	require.NoError(t, err)
	keyringPath := filepath.Join(confPath, "ceph.client.smb.fs.cluster.files.keyring")
	data, err := os.ReadFile(keyringPath)
	require.NoError(t, err)
	assert.Equal(t, "[client.smb.fs.cluster.files]\\nkey = key\\n", string(data))
}

func TestSMBServicePlacementDbUpdateAddsNewService(t *testing.T) {
	placement := &SMBServicePlacement{
		ClusterID: "files",
		ConfigURI: "rados://.smb/files/config.smb",
	}
	state := mocks.NewStateInterface(t)
	databaseMock := mocks.NewGroupedServiceQueryIntf(t)
	ctx := context.Background()
	databaseMock.On("ExistsOnHost", []interface{}{ctx, state, "smb", "files"}...).Return(false, nil).Once()
	databaseMock.On(
		"AddNew",
		[]interface{}{
			ctx,
			state,
			"smb",
			"files",
			database.SMBServiceGroupConfig{},
			database.SMBServiceInfo{ConfigURI: "rados://.smb/files/config.smb"},
		}...,
	).Return(nil).Once()
	originalDatabase := database.GroupedServicesQuery
	defer func() {
		database.GroupedServicesQuery = originalDatabase
	}()
	database.GroupedServicesQuery = databaseMock

	err := placement.DbUpdate(ctx, state)

	assert.NoError(t, err)
}

func TestDisableSMBStopsServiceAndRemovesLocalState(t *testing.T) {
	tempDir := t.TempDir()
	confPath := filepath.Join(tempDir, "conf")
	runtimePath := filepath.Join(tempDir, "samba")
	keyringPath := filepath.Join(confPath, "ceph.client.smb.fs.cluster.files.keyring")

	originalPaths := constants.GetPathConst
	defer func() {
		constants.GetPathConst = originalPaths
	}()
	constants.GetPathConst = func() constants.PathConst {
		return constants.PathConst{ConfPath: confPath}
	}

	err := os.MkdirAll(filepath.Join(confPath, "samba"), 0700)
	require.NoError(t, err)
	err = os.MkdirAll(runtimePath, 0700)
	require.NoError(t, err)
	err = os.WriteFile(keyringPath, []byte("keyring"), 0600)
	require.NoError(t, err)

	state := mocks.NewStateInterface(t)
	databaseMock := mocks.NewGroupedServiceQueryIntf(t)
	databaseMock.On("ExistsOnHost", []interface{}{context.Background(), state, "smb", "files"}...).Return(true, nil).Once()
	databaseMock.On("RemoveForHost", []interface{}{context.Background(), state, "smb", "files"}...).Return(nil).Once()
	originalDatabase := database.GroupedServicesQuery
	defer func() {
		database.GroupedServicesQuery = originalDatabase
	}()
	database.GroupedServicesQuery = databaseMock

	runner := mocks.NewRunner(t)
	runner.On("RunCommand", "snapctl", "stop", "microceph.smbd", "--disable").Return("ok", nil).Once()
	originalRunner := common.ProcessExec
	defer func() {
		common.ProcessExec = originalRunner
	}()
	common.ProcessExec = runner

	err = DisableSMB(context.Background(), state, "files")

	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(confPath, "samba"))
	assert.NoDirExists(t, runtimePath)
	assert.NoFileExists(t, keyringPath)
}

func TestValidateSMBContainerConfigRejectsNonDirectVFS(t *testing.T) {
	tests := []struct {
		name   string
		config string
	}{
		{
			name: "proxy",
			config: `{
				"shares": {
					"files": {
						"options": {
							"vfs objects": "acl_xattr ceph_new",
							"ceph_new:proxy": "yes"
						}
					}
				}
			}`,
		},
		{
			name: "missing proxy setting",
			config: `{
				"shares": {
					"files": {
						"options": {
							"vfs objects": "acl_xattr ceph_new"
						}
					}
				}
			}`,
		},
		{
			name: "classic",
			config: `{
				"shares": {
					"files": {
						"options": {
							"vfs objects": "acl_xattr ceph"
						}
					}
				}
			}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateSMBContainerConfig([]byte(test.config))

			assert.ErrorContains(t, err, "direct samba-vfs/new")
		})
	}
}
