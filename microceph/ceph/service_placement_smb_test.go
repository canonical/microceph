package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
		"provider": "samba-vfs/new",
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

func TestSMBServicePlacementPopulateParamsAcceptsNestedUpstreamSMBSpec(t *testing.T) {
	placement := &SMBServicePlacement{}
	payload := `{
		"service_type": "smb",
		"service_id": "files",
		"service_name": "smb.files",
		"placement": {
			"hosts": ["node-a"],
			"count": 1
		},
		"spec": {
			"cluster_id": "files",
			"config_uri": "rados://.smb/files/config.smb",
			"user_sources": [
				"rados:mon-config-key:smb/config/files/users-groups.0.json"
			]
		}
	}`

	err := placement.PopulateParams(nil, payload)

	require.NoError(t, err)
	assert.Equal(t, "files", placement.ClusterID)
	assert.Equal(t, "rados://.smb/files/config.smb", placement.ConfigURI)
	assert.Equal(t, []string{"rados:mon-config-key:smb/config/files/users-groups.0.json"}, placement.UserSources)

	carrier, ok := any(placement).(interface{ upstreamSpecJSON() []byte })
	require.True(t, ok, "SMB placement must retain the complete upstream SMBSpec")
	assert.JSONEq(t, payload, string(carrier.upstreamSpecJSON()))
}

func TestSMBServicePlacementPopulateParamsAcceptsClusteredMetadata(t *testing.T) {
	placement := &SMBServicePlacement{}
	payload := `{
		"service_spec": {
			"service_type": "smb",
			"service_id": "files",
			"placement": {"hosts": ["node-a", "node-b"], "count": 2},
			"spec": {
				"cluster_id": "files",
				"config_uri": "rados://.smb/files/config.smb",
				"features": ["clustered"],
				"cluster_meta_uri": "rados://.smb/files/cluster.meta.json",
				"cluster_lock_uri": "rados://.smb/files/cluster.meta.lock",
				"bind_addrs": [{"network": "10.0.0.0/24"}],
				"custom_ports": {"smb": 1445}
			}
		},
		"microceph": {
			"ctdb": {
				"rank": 1,
				"identity": "smb.files.node-b"
			},
			"ctdb_ranks": {"node-a": 0, "node-b": 1},
			"next_ctdb_rank": 2
		}
	}`

	err := placement.PopulateParams(nil, payload)

	require.NoError(t, err)
	assert.Equal(t, []string{"clustered"}, placement.Features)
	assert.Equal(t, "rados://.smb/files/cluster.meta.json", placement.ClusterMetaURI)
	assert.Equal(t, "rados://.smb/files/cluster.meta.lock", placement.ClusterLockURI)
	require.NotNil(t, placement.ctdb)
	assert.Equal(t, 1, placement.ctdb.Rank)
	assert.Equal(t, "smb.files.node-b", placement.ctdb.Identity)
	assert.Equal(t, map[string]int{"node-a": 0, "node-b": 1}, placement.ctdbRanks)
	assert.Equal(t, 2, placement.nextCTDBRank)
	assert.Equal(t, []smbBindAddress{{Network: "10.0.0.0/24"}}, placement.BindAddrs)
	assert.Equal(t, map[string]int{"smb": 1445}, placement.CustomPorts)
	assert.JSONEq(t, `{
		"service_type": "smb",
		"service_id": "files",
		"placement": {"hosts": ["node-a", "node-b"], "count": 2},
		"spec": {
			"cluster_id": "files",
			"config_uri": "rados://.smb/files/config.smb",
			"features": ["clustered"],
			"cluster_meta_uri": "rados://.smb/files/cluster.meta.json",
			"cluster_lock_uri": "rados://.smb/files/cluster.meta.lock",
			"bind_addrs": [{"network": "10.0.0.0/24"}],
			"custom_ports": {"smb": 1445}
		}
	}`, string(placement.upstreamSpecJSON()))
}

func TestSMBServicePlacementPopulateParamsRejectsClusteredWithoutMetadata(t *testing.T) {
	placement := &SMBServicePlacement{}
	payload := `{
		"cluster_id": "files",
		"config_uri": "rados://.smb/files/config.smb",
		"features": ["clustered"],
		"cluster_meta_uri": "rados://.smb/files/cluster.meta.json",
		"cluster_lock_uri": "rados://.smb/files/cluster.meta.lock"
	}`

	err := placement.PopulateParams(nil, payload)

	assert.ErrorContains(t, err, "requires CTDB node metadata")
}

func TestSMBServicePlacementNonclusteredRejectsCTDBURIs(t *testing.T) {
	for _, field := range []string{"cluster_meta_uri", "cluster_lock_uri"} {
		placement := &SMBServicePlacement{}
		payload := fmt.Sprintf(`{"cluster_id":"files","config_uri":"rados://.smb/files/config.smb",%q:"rados://.smb/files/cluster.meta.json"}`, field)
		require.ErrorContains(t, placement.PopulateParams(nil, payload), "requires the clustered SMB feature")
	}
}

func TestSMBServicePlacementPopulateParamsRejectsInvalidNetworkOptions(t *testing.T) {
	tests := []struct {
		name     string
		options  string
		expected string
	}{
		{
			name:     "bind entry without selector",
			options:  `"bind_addrs": [{}]`,
			expected: "must set exactly one",
		},
		{
			name:     "bind entry with address and network",
			options:  `"bind_addrs": [{"address":"192.0.2.10","network":"192.0.2.0/24"}]`,
			expected: "must set exactly one",
		},
		{
			name:     "out of range port",
			options:  `"custom_ports": {"smb": 65536}`,
			expected: "custom port smb is invalid",
		},
		{
			name:     "unsupported port name",
			options:  `"custom_ports": {"metrics": 9922}`,
			expected: "does not support custom port 'metrics'",
		},
		{
			name:     "custom CTDB port",
			options:  `"custom_ports": {"ctdb": 14379}`,
			expected: "does not support a custom CTDB port",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			placement := &SMBServicePlacement{}
			payload := fmt.Sprintf(`{
				"cluster_id": "files",
				"config_uri": "rados://.smb/files/config.smb",
				%s
			}`, test.options)

			err := placement.PopulateParams(nil, payload)

			assert.ErrorContains(t, err, test.expected)
		})
	}
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

func TestSMBServicePlacementPopulateParamsAcceptsUpstreamSpecWithoutProvider(t *testing.T) {
	placement := &SMBServicePlacement{}
	payload := `{
		"service_type": "smb",
		"service_id": "files",
		"spec": {
			"cluster_id": "files",
			"config_uri": "rados://.smb/files/config.smb"
		}
	}`

	err := placement.PopulateParams(nil, payload)

	assert.NoError(t, err)
}

func TestSMBServicePlacementPopulateParamsRejectsOtherClusterNamespace(t *testing.T) {
	placement := &SMBServicePlacement{}
	payload := `{
		"cluster_id": "files",
		"config_uri": "rados://.smb/other/config.smb",
		"provider": "samba-vfs/new"
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
	runner.On("RunCommandContext", mock.Anything, "snapctl", "is-connected", "smb-identity").Return("", assert.AnError).Once()
	originalRunner := common.ProcessExec
	defer func() {
		common.ProcessExec = originalRunner
	}()
	common.ProcessExec = runner

	placement := &SMBServicePlacement{ClusterID: "files"}
	err := placement.HospitalityCheck(nil)

	assert.ErrorContains(t, err, "requires the smb-identity interface connection")
}

func TestResolveSMBCTDBAddressUsesMicroClusterAddress(t *testing.T) {
	url := api.NewURL()
	url.Host("10.10.10.12:7443")
	state := mocks.NewStateInterface(t)
	state.On("ClusterState").Return(&mocks.MockState{URL: url}).Once()

	address, err := resolveSMBCTDBAddress(state)

	require.NoError(t, err)
	assert.Equal(t, "10.10.10.12", address)
}

func TestResolveSMBBindAddressDefaultsToPublicNetwork(t *testing.T) {
	originalFetchConfig := fetchConfigDb
	defer func() {
		fetchConfigDb = originalFetchConfig
	}()
	fetchConfigDb = func(_ context.Context, _ interfaces.StateInterface) (map[string]string, error) {
		return map[string]string{"public_network": "10.0.0.0/24,192.0.2.0/24"}, nil
	}

	network := mocks.NewNetworkIntf(t)
	network.On("FindIpOnSubnet", "10.0.0.0/24,192.0.2.0/24").Return("10.0.0.12", nil).Once()
	originalNetwork := common.Network
	defer func() {
		common.Network = originalNetwork
	}()
	common.Network = network

	address, err := resolveSMBBindAddress(context.Background(), nil, &SMBServicePlacement{})

	require.NoError(t, err)
	assert.Equal(t, "10.0.0.12", address)
}

func TestResolveSMBBindAddressUsesFirstResolvableBind(t *testing.T) {
	network := mocks.NewNetworkIntf(t)
	network.On("FindIpOnSubnet", "198.51.100.0/24").Return("", assert.AnError).Once()
	network.On("FindNetworkAddress", "192.0.2.12").Return("192.0.2.0/24", nil).Once()
	originalNetwork := common.Network
	defer func() {
		common.Network = originalNetwork
	}()
	common.Network = network

	placement := &SMBServicePlacement{
		BindAddrs: []smbBindAddress{
			{Network: "198.51.100.0/24"},
			{Address: "192.0.2.12"},
			{Network: "203.0.113.0/24"},
		},
	}
	address, err := resolveSMBBindAddress(context.Background(), nil, placement)

	require.NoError(t, err)
	assert.Equal(t, "192.0.2.12", address)
}

func TestResolveSMBBindAddressReportsCandidateFailure(t *testing.T) {
	network := mocks.NewNetworkIntf(t)
	network.On("FindIpOnSubnet", "198.51.100.0/24").Return("", assert.AnError).Once()
	network.On("FindNetworkAddress", "not-an-address").Return("", assert.AnError).Once()
	originalNetwork := common.Network
	defer func() {
		common.Network = originalNetwork
	}()
	common.Network = network

	placement := &SMBServicePlacement{
		BindAddrs: []smbBindAddress{
			{Network: "198.51.100.0/24"},
			{Address: "not-an-address"},
		},
	}
	_, err := resolveSMBBindAddress(context.Background(), nil, placement)

	assert.ErrorContains(t, err, "failed to resolve an SMB bind address")
}

func TestSMBServicePlacementClusteredHospitalityRequiresCTDBRun(t *testing.T) {
	runner := mocks.NewRunner(t)
	runner.On("RunCommandContext", mock.Anything, "snapctl", "is-connected", "smb-identity").Return("", nil).Once()
	runner.On("RunCommandContext", mock.Anything, "snapctl", "is-connected", "ctdb-run").Return("", assert.AnError).Once()
	originalRunner := common.ProcessExec
	defer func() {
		common.ProcessExec = originalRunner
	}()
	common.ProcessExec = runner

	placement := &SMBServicePlacement{ClusterID: "files", Features: []string{"clustered"}}
	err := placement.HospitalityCheck(nil)

	assert.ErrorContains(t, err, "requires the ctdb-run interface connection")
}

func TestSMBServicePlacementServiceInitMaterializesConfigAndStartsSMBD(t *testing.T) {
	tempDir := t.TempDir()
	confPath := filepath.Join(tempDir, "conf")
	mockSMBPublicAddress(t, "192.0.2.0/24", "192.0.2.12")

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
	fetchSMBSourceFunc = func(_ context.Context, _ string) ([]byte, error) {
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
	runner.On("RunCommandContext", mock.Anything, "ceph", "auth", "get", "client.smb.fs.cluster.files").Return("[client.smb.fs.cluster.files]\\nkey = key\\n", nil).Once()
	runner.On("RunCommandContext", mock.Anything, "snapctl", "services", "microceph.smbd").Return("microceph.smbd disabled inactive", nil).Once()
	runner.On("RunCommandContext", mock.Anything, "snapctl", "start", "microceph.smbd", "--enable").Return("ok", nil).Once()
	originalRunner := common.ProcessExec
	defer func() {
		common.ProcessExec = originalRunner
	}()
	common.ProcessExec = runner

	placement := &SMBServicePlacement{
		ClusterID:   "files",
		ConfigURI:   "rados://.smb/files/config.smb",
		CustomPorts: map[string]int{"smb": 1445},
	}

	err := placement.ServiceInit(context.Background(), nil)

	require.NoError(t, err)
	keyringPath := filepath.Join(confPath, "ceph.client.smb.fs.cluster.files.keyring")
	data, err := os.ReadFile(keyringPath)
	require.NoError(t, err)
	assert.Equal(t, "[client.smb.fs.cluster.files]\\nkey = key\\n", string(data))
	baseConfig := readSMBConfigFile(t, filepath.Join(confPath, "samba", "smb.conf"))
	assert.Contains(t, baseConfig, "bind interfaces only = yes\ninterfaces = 192.0.2.12\nsmb ports = 1445\n")
}

func TestSMBServicePlacementFreshInitFailureRemovesLocalState(t *testing.T) {
	tempDir := t.TempDir()
	confPath := filepath.Join(tempDir, "conf")
	runtimePath := filepath.Join(tempDir, "samba")
	mockSMBPublicAddress(t, "192.0.2.0/24", "192.0.2.12")

	originalPaths := constants.GetPathConst
	defer func() {
		constants.GetPathConst = originalPaths
	}()
	constants.GetPathConst = func() constants.PathConst {
		return constants.PathConst{ConfPath: confPath, DataPath: filepath.Join(tempDir, "data")}
	}

	originalFetch := fetchSMBSourceFunc
	defer func() {
		fetchSMBSourceFunc = originalFetch
	}()
	fetchSMBSourceFunc = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`{"shares":{"files":{"options":{"vfs objects":"ceph_new","ceph_new:proxy":"no"}}}}`), nil
	}

	runner := mocks.NewRunner(t)
	runner.On("RunCommandContext", mock.Anything, "ceph", "auth", "get", "client.smb.fs.cluster.files").
		Return("key", nil).Once()
	runner.On("RunCommandContext", mock.Anything, "snapctl", "services", "microceph.smbd").
		Return("microceph.smbd disabled inactive", nil).Once()
	runner.On("RunCommandContext", mock.Anything, "snapctl", "start", "microceph.smbd", "--enable").
		Return("", assert.AnError).Once()
	runner.On("RunCommandContext", mock.Anything, "snapctl", "stop", "microceph.smbd", "--disable").Return("", nil).Once()
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

	assert.ErrorContains(t, err, "failed to start SMB service smbd")
	assert.NoDirExists(t, filepath.Join(confPath, "samba"))
	assert.NoDirExists(t, runtimePath)
	assert.NoFileExists(t, filepath.Join(confPath, "ceph.client.smb.fs.cluster.files.keyring"))
}

func TestSMBServicePlacementRejectsModeTransitionsBeforeIO(t *testing.T) {
	for _, wasClustered := range []bool{false, true} {
		t.Run(fmt.Sprint(wasClustered), func(t *testing.T) {
			root := t.TempDir()
			runtimePath := filepath.Join(root, "samba")
			require.NoError(t, os.MkdirAll(runtimePath, 0700))
			require.NoError(t, os.WriteFile(filepath.Join(runtimePath, "cluster-id"), []byte("files\n"), 0600))
			if wasClustered {
				require.NoError(t, os.WriteFile(filepath.Join(runtimePath, "ctdb.json"), []byte("original"), 0600))
			}
			originalPaths := constants.GetPathConst
			t.Cleanup(func() { constants.GetPathConst = originalPaths })
			constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: filepath.Join(root, "conf")} }
			placement := &SMBServicePlacement{ClusterID: "files", ConfigURI: "rados://.smb/files/config.smb", recordedClustered: &wasClustered}
			if !wasClustered {
				placement.Features = []string{"clustered"}
			}
			err := placement.ServiceInit(context.Background(), nil)
			require.ErrorContains(t, err, "cannot be changed")
			require.Equal(t, "files\n", readSMBConfigFile(t, filepath.Join(runtimePath, "cluster-id")))
			if wasClustered {
				require.Equal(t, "original", readSMBConfigFile(t, filepath.Join(runtimePath, "ctdb.json")))
			}
		})
	}
}

func TestSMBServicePlacementFreshClusteredStartsCTDBBeforeSMBD(t *testing.T) {
	tempDir := t.TempDir()
	confPath := filepath.Join(tempDir, "conf")
	runtimePath := filepath.Join(tempDir, "samba")
	t.Setenv("SNAP", "/snap/microceph/current")

	originalPaths := constants.GetPathConst
	defer func() {
		constants.GetPathConst = originalPaths
	}()
	constants.GetPathConst = func() constants.PathConst {
		return constants.PathConst{ConfPath: confPath, DataPath: filepath.Join(tempDir, "data")}
	}
	err := os.MkdirAll(runtimePath, 0700)
	require.NoError(t, err)

	originalFetch := fetchSMBSourceFunc
	defer func() {
		fetchSMBSourceFunc = originalFetch
	}()
	fetchSMBSourceFunc = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`{
			"samba-container-config": "v0",
			"configs": {"files": {"instance_features": ["ctdb"]}},
			"shares": {"files": {"options": {
				"vfs objects": "acl_xattr ceph_new",
				"ceph_new:proxy": "no"
			}}}
		}`), nil
	}

	originalFetchConfig := fetchConfigDb
	defer func() {
		fetchConfigDb = originalFetchConfig
	}()
	fetchConfigDb = func(_ context.Context, _ interfaces.StateInterface) (map[string]string, error) {
		return map[string]string{"public_network": "10.0.0.0/24"}, nil
	}

	network := mocks.NewNetworkIntf(t)
	network.On("FindIpOnSubnet", "10.0.0.0/24").Return("10.0.0.12", nil).Once()
	originalNetwork := common.Network
	defer func() {
		common.Network = originalNetwork
	}()
	common.Network = network

	runner := mocks.NewRunner(t)
	runner.On("RunCommandContext", mock.Anything, "ceph", "auth", "get", "client.smb.fs.cluster.files").Return("[client.smb.fs.cluster.files]\\nkey = data\\n", nil).Once()
	runner.On("RunCommandContext", mock.Anything,

		"ceph",
		"auth",
		"get-or-create",
		"client.smb.config.files",
		"mon",
		"allow r",
		"osd",
		"allow rwx pool=.smb namespace=files object_prefix cluster.meta.").
		Return("[client.smb.config.files]\\nkey = config\\n", nil).Once()
	for _, service := range []string{"ctdbd", "ctdb-nodes"} {
		runner.On("RunCommandContext", mock.Anything, "snapctl", "services", "microceph."+service).Return("microceph."+service+" disabled inactive", nil).Once()
		runner.On("RunCommandContext", mock.Anything, "snapctl", "start", "microceph."+service, "--enable").Return("ok", nil).Once()
	}
	runner.On("RunCommandContext", mock.Anything, "snapctl", "services", "microceph.smbd").Return("microceph.smbd enabled active", nil).Once()
	runner.On("RunCommandContext", mock.Anything, "snapctl", "restart", "microceph.smbd").Return("ok", nil).Once()
	originalRunner := common.ProcessExec
	defer func() {
		common.ProcessExec = originalRunner
	}()
	common.ProcessExec = runner

	placement := &SMBServicePlacement{
		ClusterID:      "files",
		ConfigURI:      "rados://.smb/files/config.smb",
		Features:       []string{"clustered"},
		ClusterMetaURI: "rados://.smb/files/cluster.meta.json",
		ClusterLockURI: "rados://.smb/files/cluster.meta.lock",
		CustomPorts:    map[string]int{"smb": 1445},
		ctdb: &smbCTDBPlacement{
			Rank:     1,
			Identity: "smb.files.node-b",
		},
	}

	url := api.NewURL()
	url.Host("10.10.10.12:7443")
	state := mocks.NewStateInterface(t)
	state.On("ClusterState").Return(&mocks.MockState{URL: url}).Once()
	err = placement.ServiceInit(context.Background(), state)

	require.NoError(t, err)
	assert.Equal(t, "10.10.10.12\n", readSMBConfigFile(t, filepath.Join(runtimePath, "ctdb-address")))
	baseConfig := readSMBConfigFile(t, filepath.Join(confPath, "samba", "smb.conf"))
	assert.Contains(t, baseConfig, "bind interfaces only = yes\ninterfaces = 10.0.0.12\nsmb ports = 1445\n")
	ctdbConfig := readSMBConfigFile(t, filepath.Join(tempDir, "data", "samba", "smb.ctdb.conf"))
	assert.Equal(t, "[global]\nctdbd socket = /run/ctdb/ctdbd.socket\nbind interfaces only = yes\ninterfaces = 10.0.0.12\nsmb ports = 1445\n", ctdbConfig)
	configKeyring, err := os.ReadFile(filepath.Join(confPath, "ceph.client.smb.config.files.keyring"))
	require.NoError(t, err)
	assert.Equal(t, "[client.smb.config.files]\\nkey = config\\n", string(configKeyring))
}

func TestStartOrRestartSMBServicesRollsBackNewServices(t *testing.T) {
	runner := mocks.NewRunner(t)
	for _, service := range []string{"ctdbd", "ctdb-nodes", "smbd"} {
		runner.On("RunCommandContext", mock.Anything, "snapctl", "services", "microceph."+service).
			Return("microceph."+service+" disabled inactive", nil).Once()
		if service == "smbd" {
			runner.On("RunCommandContext", mock.Anything, "snapctl", "start", "microceph.smbd", "--enable").
				Return("", assert.AnError).Once()
			continue
		}
		runner.On("RunCommandContext", mock.Anything, "snapctl", "start", "microceph."+service, "--enable").
			Return("ok", nil).Once()
	}
	runner.On("RunCommandContext", mock.Anything, "snapctl", "stop", "microceph.ctdb-nodes", "--disable").
		Return("ok", nil).Once()
	runner.On("RunCommandContext", mock.Anything, "snapctl", "stop", "microceph.ctdbd", "--disable").
		Return("ok", nil).Once()
	originalRunner := common.ProcessExec
	defer func() {
		common.ProcessExec = originalRunner
	}()
	common.ProcessExec = runner

	err := startOrRestartSMBServices(context.Background(), []string{"ctdbd", "ctdb-nodes", "smbd"})

	assert.ErrorContains(t, err, "failed to start SMB service smbd")
}

func TestStartOrRestartSMBServicesKeepsPreviouslyActiveServicesOnFailure(t *testing.T) {
	runner := mocks.NewRunner(t)
	runner.On("RunCommandContext", mock.Anything, "snapctl", "services", "microceph.ctdbd").
		Return("microceph.ctdbd enabled active", nil).Once()
	runner.On("RunCommandContext", mock.Anything, "snapctl", "restart", "microceph.ctdbd").
		Return("ok", nil).Once()
	runner.On("RunCommandContext", mock.Anything, "snapctl", "services", "microceph.ctdb-nodes").
		Return("microceph.ctdb-nodes disabled inactive", nil).Once()
	runner.On("RunCommandContext", mock.Anything, "snapctl", "start", "microceph.ctdb-nodes", "--enable").
		Return("", assert.AnError).Once()
	originalRunner := common.ProcessExec
	defer func() {
		common.ProcessExec = originalRunner
	}()
	common.ProcessExec = runner

	err := startOrRestartSMBServices(context.Background(), []string{"ctdbd", "ctdb-nodes", "smbd"})

	assert.ErrorContains(t, err, "failed to start SMB service ctdb-nodes")
}

func TestSMBServicePlacementClusteredPostCheckVerifiesAllServices(t *testing.T) {
	originalCheck := smbPostPlacementCheckFunc
	defer func() {
		smbPostPlacementCheckFunc = originalCheck
	}()
	checked := []string{}
	smbPostPlacementCheckFunc = func(_ context.Context, service string) error {
		checked = append(checked, service)
		return nil
	}
	t.Setenv("SNAP", "/snap/microceph/current")
	runner := mocks.NewRunner(t)
	runner.On("RunCommandContext", mock.Anything, "/snap/microceph/current/commands/samba-command", "ctdb", "pnn").Return("0\n", nil).Once()
	originalRunner := common.ProcessExec
	t.Cleanup(func() { common.ProcessExec = originalRunner })
	common.ProcessExec = runner
	placement := &SMBServicePlacement{Features: []string{"clustered"}, ctdb: &smbCTDBPlacement{Rank: 0}}

	err := placement.PostPlacementCheck(nil)

	require.NoError(t, err)
	assert.Equal(t, []string{"ctdbd", "ctdb-nodes", "smbd"}, checked)
}

func TestSMBServicePlacementDbUpdatePersistsCompleteUpstreamSpec(t *testing.T) {
	payload := `{
		"service_type": "smb",
		"service_id": "files",
		"service_name": "smb.files",
		"placement": {"hosts": ["node-a", "node-b"]},
		"spec": {
			"cluster_id": "files",
			"config_uri": "rados://.smb/files/config.smb",
			"user_sources": ["rados:mon-config-key:smb/config/files/users-groups.0.json"],
			"provider": "samba-vfs/new"
		}
	}`
	placement := &SMBServicePlacement{}
	err := placement.PopulateParams(nil, payload)
	require.NoError(t, err)

	expectedGroupConfig, err := json.Marshal(struct {
		DesiredSpec json.RawMessage `json:"desired_spec"`
	}{DesiredSpec: json.RawMessage(payload)})
	require.NoError(t, err)

	state := mocks.NewStateInterface(t)
	databaseMock := mocks.NewGroupedServiceQueryIntf(t)
	ctx := context.Background()
	databaseMock.On(
		"AddOrUpdate",
		ctx,
		state,
		"smb",
		"files",
		mock.Anything,
		mock.Anything,
	).Run(func(args mock.Arguments) {
		info, ok := args.Get(5).(database.SMBServiceInfo)
		require.True(t, ok)
		assert.JSONEq(t, payload, string(info.AppliedSpec))
		assert.Equal(t, placement.configDigest, info.ConfigDigest)
		groupConfig, ok := args.Get(4).(database.SMBServiceGroupConfig)
		require.True(t, ok)

		groupConfigJSON, err := json.Marshal(groupConfig)
		require.NoError(t, err)
		assert.JSONEq(t, string(expectedGroupConfig), string(groupConfigJSON))
	}).Return(nil).Once()
	originalDatabase := database.GroupedServicesQuery
	defer func() {
		database.GroupedServicesQuery = originalDatabase
	}()
	database.GroupedServicesQuery = databaseMock

	placement.configDigest = "applied-digest"
	err = placement.DbUpdate(ctx, state)

	assert.NoError(t, err)
}

func TestSMBServicePlacementDbUpdateRecordsCTDBRanksAndReceipt(t *testing.T) {
	payload := `{"service_spec":{"spec":{"cluster_id":"files","config_uri":"rados://.smb/files/config.smb","features":["clustered"],"cluster_meta_uri":"rados://.smb/files/cluster.meta.json","cluster_lock_uri":"rados://.smb/files/cluster.meta.lock"}},"microceph":{"ctdb":{"rank":0,"identity":"smb.files.node-a"},"ctdb_ranks":{"node-a":0,"node-b":1},"next_ctdb_rank":2}}`
	placement := &SMBServicePlacement{}
	require.NoError(t, placement.PopulateParams(nil, payload))
	placement.configDigest = "successful-apply"
	ctx := context.Background()
	state := mocks.NewStateInterface(t)
	db := mocks.NewGroupedServiceQueryIntf(t)
	db.On("AddOrUpdate", ctx, state, "smb", "files", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		group := args.Get(4).(database.SMBServiceGroupConfig)
		assert.Equal(t, map[string]int{"node-a": 0, "node-b": 1}, group.CTDBRanks)
		assert.Equal(t, 2, group.NextCTDBRank)
		info := args.Get(5).(database.SMBServiceInfo)
		require.NotNil(t, info.CTDBRank)
		assert.Equal(t, 0, *info.CTDBRank)
		assert.Equal(t, "smb.files.node-a", info.CTDBIdentity)
		assert.Equal(t, "successful-apply", info.ConfigDigest)
		assert.JSONEq(t, string(placement.upstreamSpecJSON()), string(info.AppliedSpec))
	}).Return(nil).Once()
	original := database.GroupedServicesQuery
	t.Cleanup(func() { database.GroupedServicesQuery = original })
	database.GroupedServicesQuery = db
	require.NoError(t, placement.DbUpdate(ctx, state))
}

func TestDisableSMBStopsServiceAndRemovesLocalState(t *testing.T) {
	stubSMBRecordedMode(t, nil)
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
	runner.On("RunCommandContext", mock.Anything, "snapctl", "stop", "microceph.smbd", "--disable").Return("ok", nil).Once()
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

func expectSMBRetirement(t *testing.T) {
	t.Helper()
	stubSMBRecordedMode(t, nil)
	original := retireSMBCTDBMemberFunc
	called := false
	t.Cleanup(func() { retireSMBCTDBMemberFunc = original; require.True(t, called) })
	retireSMBCTDBMemberFunc = func(ctx context.Context, clusterID string) error {
		require.NoError(t, ctx.Err())
		require.Equal(t, "files", clusterID)
		require.True(t, isSMBClusteredLocal(), "metadata must remain until retirement completes")
		called = true
		return nil
	}
}

func TestDisableSMBCompletesWhenClusteredAuthCleanupFails(t *testing.T) {
	expectSMBRetirement(t)
	tempDir := t.TempDir()
	confPath := filepath.Join(tempDir, "conf")
	dataPath := filepath.Join(tempDir, "data")
	runtimePath := filepath.Join(tempDir, "samba")
	ctdbIncludePath := filepath.Join(dataPath, "samba", "smb.ctdb.conf")
	dataKeyringPath := filepath.Join(confPath, "ceph.client.smb.fs.cluster.files.keyring")
	configKeyringPath := filepath.Join(confPath, "ceph.client.smb.config.files.keyring")

	originalPaths := constants.GetPathConst
	defer func() {
		constants.GetPathConst = originalPaths
	}()
	constants.GetPathConst = func() constants.PathConst {
		return constants.PathConst{ConfPath: confPath, DataPath: dataPath}
	}

	err := os.MkdirAll(filepath.Join(confPath, "samba"), 0700)
	require.NoError(t, err)
	err = os.MkdirAll(runtimePath, 0700)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(runtimePath, "ctdb.json"), []byte("{}"), 0600)
	require.NoError(t, err)
	err = os.MkdirAll(filepath.Dir(ctdbIncludePath), 0700)
	require.NoError(t, err)
	err = os.WriteFile(ctdbIncludePath, []byte("stale"), 0600)
	require.NoError(t, err)
	for _, path := range []string{dataKeyringPath, configKeyringPath} {
		err = os.WriteFile(path, []byte("keyring"), 0600)
		require.NoError(t, err)
	}

	state := mocks.NewStateInterface(t)
	databaseMock := mocks.NewGroupedServiceQueryIntf(t)
	databaseMock.On("ExistsOnHost", []interface{}{context.Background(), state, "smb", "files"}...).Return(true, nil).Once()
	databaseMock.On("RemoveForHost", []interface{}{context.Background(), state, "smb", "files"}...).Return(nil).Once()
	databaseMock.On("GetGroupedServices", context.Background(), state).Return([]database.GroupedService{}, nil).Once()
	originalDatabase := database.GroupedServicesQuery
	defer func() {
		database.GroupedServicesQuery = originalDatabase
	}()
	database.GroupedServicesQuery = databaseMock

	runner := mocks.NewRunner(t)
	for _, service := range []string{"smbd", "ctdb-nodes", "ctdbd"} {
		runner.On("RunCommandContext", mock.Anything, "snapctl", "stop", "microceph."+service, "--disable").Return("ok", nil).Once()
	}
	runner.On("RunCommandContext", mock.Anything, "ceph", "auth", "del", "client.smb.config.files").Return("", assert.AnError).Once()
	originalRunner := common.ProcessExec
	defer func() {
		common.ProcessExec = originalRunner
	}()
	common.ProcessExec = runner

	err = DisableSMB(context.Background(), state, "files")

	require.NoError(t, err)
	assert.NoFileExists(t, dataKeyringPath)
	assert.NoFileExists(t, configKeyringPath)
	assert.NoFileExists(t, ctdbIncludePath)
}

func TestDisableSMBOnOneMemberKeepsSharedConfigurationIdentity(t *testing.T) {
	expectSMBRetirement(t)
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
	err := os.MkdirAll(runtimePath, 0700)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(runtimePath, "ctdb.json"), []byte("{}"), 0600)
	require.NoError(t, err)

	state := mocks.NewStateInterface(t)
	databaseMock := mocks.NewGroupedServiceQueryIntf(t)
	ctx := context.Background()
	databaseMock.On("ExistsOnHost", ctx, state, "smb", "files").Return(true, nil).Once()
	databaseMock.On("RemoveForHost", ctx, state, "smb", "files").Return(nil).Once()
	databaseMock.On("GetGroupedServices", ctx, state).Return([]database.GroupedService{
		{Service: "smb", GroupID: "files", Member: "node-b"},
	}, nil).Once()
	originalDatabase := database.GroupedServicesQuery
	defer func() {
		database.GroupedServicesQuery = originalDatabase
	}()
	database.GroupedServicesQuery = databaseMock

	runner := mocks.NewRunner(t)
	for _, service := range []string{"smbd", "ctdb-nodes", "ctdbd"} {
		runner.On("RunCommandContext", mock.Anything, "snapctl", "stop", "microceph."+service, "--disable").
			Return("ok", nil).Once()
	}
	originalRunner := common.ProcessExec
	defer func() {
		common.ProcessExec = originalRunner
	}()
	common.ProcessExec = runner

	err = DisableSMB(ctx, state, "files")

	require.NoError(t, err)
	assert.NoDirExists(t, runtimePath)
}

func TestSMBPlacementReceiptSkipsOnlyCurrentEffectiveConfig(t *testing.T) {
	paths := constants.GetPathConst
	t.Cleanup(func() { constants.GetPathConst = paths })
	root := t.TempDir()
	constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: filepath.Join(root, "conf")} }
	runtimeDir := filepath.Join(root, "samba")
	require.NoError(t, os.MkdirAll(runtimeDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "cluster-id"), []byte("files\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "container.json"), []byte("{}"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "conf", "samba"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "conf", "samba", "smb.conf"), []byte("config"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "conf", "ceph.client.smb.fs.cluster.files.keyring"), []byte("key"), 0600))
	mockSMBPublicAddress(t, "192.0.2.0/24", "192.0.2.12")
	// The same URI can refer to new shares or user credentials.
	container := []byte(`{"shares":{"files":{"options":{"vfs objects":"ceph_new","ceph_new:proxy":"no"}}}}`)
	user := []byte(`{"users":["alice"]}`)
	originalFetch := fetchSMBSourceFunc
	t.Cleanup(func() { fetchSMBSourceFunc = originalFetch })
	fetchSMBSourceFunc = func(_ context.Context, uri string) ([]byte, error) {
		if uri == "rados://.smb/files/config.smb" {
			return container, nil
		}
		return user, nil
	}
	runner := mocks.NewRunner(t)
	runner.On("RunCommandContext", mock.Anything, "snapctl", "services", "microceph.smbd").Return("microceph.smbd enabled active", nil).Once()
	originalRunner := common.ProcessExec
	t.Cleanup(func() { common.ProcessExec = originalRunner })
	common.ProcessExec = runner
	payload := `{"cluster_id":"files","config_uri":"rados://.smb/files/config.smb","user_sources":["rados:mon-config-key:smb/config/files/users-groups.0.json"]}`
	placement := &SMBServicePlacement{}
	require.NoError(t, placement.PopulateParams(nil, payload))
	placement.bindAddress = "192.0.2.12"
	data, err := fetchSMBConfigSources(context.Background(), placement)
	require.NoError(t, err)
	placement.configData = data
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "container.json"), container, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "users-0.json"), user, 0600))
	digest, err := placement.effectiveConfigDigest()
	require.NoError(t, err)
	info, err := json.Marshal(database.SMBServiceInfo{AppliedSpec: placement.upstreamSpec, ConfigDigest: digest})
	require.NoError(t, err)
	state := mocks.NewStateInterface(t)
	db := mocks.NewGroupedServiceQueryIntf(t)
	db.On("GetGroupedServicesOnHost", context.Background(), state).Return([]database.GroupedService{{Service: "smb", GroupID: "files", Info: string(info)}}, nil).Times(4)
	originalDB := database.GroupedServicesQuery
	t.Cleanup(func() { database.GroupedServicesQuery = originalDB })
	database.GroupedServicesQuery = db

	fresh := &SMBServicePlacement{}
	require.NoError(t, fresh.PopulateParams(nil, payload))
	require.NoError(t, fresh.ServiceInit(context.Background(), state))
	assert.True(t, fresh.unchanged)
	originalCheck := smbPostPlacementCheckFunc
	t.Cleanup(func() { smbPostPlacementCheckFunc = originalCheck })
	smbPostPlacementCheckFunc = func(_ context.Context, service string) error { require.Equal(t, "smbd", service); return nil }
	require.NoError(t, fresh.PostPlacementCheck(state))
	db.On("AddOrUpdate", context.Background(), state, "smb", "files", mock.Anything, mock.Anything).Return(nil).Once()
	require.NoError(t, fresh.DbUpdate(context.Background(), state))

	user = []byte(`{"users":["bob"]}`)
	changed := &SMBServicePlacement{}
	require.NoError(t, changed.PopulateParams(nil, payload))
	changed.bindAddress = "192.0.2.12"
	changed.configData, err = fetchSMBConfigSources(context.Background(), changed)
	require.NoError(t, err)
	changedDigest, err := changed.effectiveConfigDigest()
	require.NoError(t, err)
	assert.NotEqual(t, digest, changedDigest)
	match, err := changed.matchesAppliedReceipt(context.Background(), state)
	require.NoError(t, err)
	assert.False(t, match)

	user = []byte(`{"users":["alice"]}`)
	container = []byte(`{"shares":{"new-share":{"options":{"vfs objects":"ceph_new","ceph_new:proxy":"no"}}}}`)
	changed.configData, err = fetchSMBConfigSources(context.Background(), changed)
	require.NoError(t, err)
	changedDigest, err = changed.effectiveConfigDigest()
	require.NoError(t, err)
	assert.NotEqual(t, digest, changedDigest)
	match, err = changed.matchesAppliedReceipt(context.Background(), state)
	require.NoError(t, err)
	assert.False(t, match)

	// A receipt does not mask missing node-local files.
	require.NoError(t, os.Remove(filepath.Join(root, "conf", "samba", "smb.conf")))
	match, err = fresh.matchesAppliedReceipt(context.Background(), state)
	require.NoError(t, err)
	assert.False(t, match)
}

func TestSMBPartialUpdateRestoresPreviousFiles(t *testing.T) {
	originalPaths := constants.GetPathConst
	t.Cleanup(func() { constants.GetPathConst = originalPaths })
	root := t.TempDir()
	confDir := filepath.Join(root, "conf")
	constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: confDir} }
	runtimeDir := filepath.Join(root, "samba")
	require.NoError(t, os.MkdirAll(runtimeDir, 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(confDir, "samba"), 0700))
	for path, contents := range map[string]string{
		filepath.Join(runtimeDir, "cluster-id"):     "files\n",
		filepath.Join(runtimeDir, "container.json"): "old container",
		filepath.Join(confDir, "samba", "smb.conf"): "old smb config",
	} {
		require.NoError(t, os.WriteFile(path, []byte(contents), 0600))
	}
	mockSMBPublicAddress(t, "192.0.2.0/24", "192.0.2.12")
	originalFetch := fetchSMBSourceFunc
	t.Cleanup(func() { fetchSMBSourceFunc = originalFetch })
	fetchSMBSourceFunc = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`{"shares":{"files":{"options":{"vfs objects":"ceph_new","ceph_new:proxy":"no"}}}}`), nil
	}
	originalWrite := writeSMBFileFunc
	t.Cleanup(func() { writeSMBFileFunc = originalWrite })
	failed := false
	writeSMBFileFunc = func(path string, data []byte, mode os.FileMode) error {
		if path == filepath.Join(runtimeDir, "container.json.tmp") && !failed {
			failed = true
			return assert.AnError
		}
		return os.WriteFile(path, data, mode)
	}
	runner := mocks.NewRunner(t)
	runner.On("RunCommandContext", mock.Anything, "snapctl", "services", "microceph.smbd").Return("active", nil).Once()
	runner.On("RunCommandContext", mock.Anything, "snapctl", "restart", "microceph.smbd").Return("ok", nil).Once()
	originalRunner := common.ProcessExec
	t.Cleanup(func() { common.ProcessExec = originalRunner })
	common.ProcessExec = runner
	state := mocks.NewStateInterface(t)
	db := mocks.NewGroupedServiceQueryIntf(t)
	db.On("GetGroupedServicesOnHost", context.Background(), state).Return([]database.GroupedService{}, nil).Once()
	originalDB := database.GroupedServicesQuery
	t.Cleanup(func() { database.GroupedServicesQuery = originalDB })
	database.GroupedServicesQuery = db
	placement := &SMBServicePlacement{ClusterID: "files", ConfigURI: "rados://.smb/files/config.smb"}
	err := placement.ServiceInit(context.Background(), state)
	assert.ErrorContains(t, err, "failed to write SMB container configuration")
	assert.Equal(t, "old smb config", readSMBConfigFile(t, filepath.Join(confDir, "samba", "smb.conf")))
	assert.Equal(t, "old container", readSMBConfigFile(t, filepath.Join(runtimeDir, "container.json")))
}

func TestSMBPlacementLegacyReceiptNeverSkips(t *testing.T) {
	placement := &SMBServicePlacement{ClusterID: "files", configDigest: "digest"}
	state := mocks.NewStateInterface(t)
	db := mocks.NewGroupedServiceQueryIntf(t)
	db.On("GetGroupedServicesOnHost", context.Background(), state).Return([]database.GroupedService{{Service: "smb", GroupID: "files", Info: `{"config_uri":"rados://.smb/files/config.smb"}`}}, nil).Once()
	original := database.GroupedServicesQuery
	t.Cleanup(func() { database.GroupedServicesQuery = original })
	database.GroupedServicesQuery = db
	matched, err := placement.matchesAppliedReceipt(context.Background(), state)
	require.NoError(t, err)
	assert.False(t, matched)
}

func TestDisableSMBCleansMatchingLocalStateWithoutMemberRecord(t *testing.T) {
	stubSMBRecordedMode(t, nil)
	root := t.TempDir()
	originalPaths := constants.GetPathConst
	t.Cleanup(func() { constants.GetPathConst = originalPaths })
	constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: filepath.Join(root, "conf")} }
	runtimeDir := filepath.Join(root, "samba")
	require.NoError(t, os.MkdirAll(runtimeDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "cluster-id"), []byte("files\n"), 0600))
	state := mocks.NewStateInterface(t)
	db := mocks.NewGroupedServiceQueryIntf(t)
	db.On("ExistsOnHost", context.Background(), state, "smb", "files").Return(false, nil).Once()
	db.On("GetGroupedServicesOnHost", context.Background(), state).Return([]database.GroupedService{}, nil).Once()
	originalDB := database.GroupedServicesQuery
	t.Cleanup(func() { database.GroupedServicesQuery = originalDB })
	database.GroupedServicesQuery = db
	runner := mocks.NewRunner(t)
	runner.On("RunCommandContext", mock.Anything, "snapctl", "stop", "microceph.smbd", "--disable").Return("", nil).Once()
	originalRunner := common.ProcessExec
	t.Cleanup(func() { common.ProcessExec = originalRunner })
	common.ProcessExec = runner
	require.NoError(t, DisableSMB(context.Background(), state, "files"))
	require.NoDirExists(t, runtimeDir)
}

func TestDisableSMBAbsentAndWrongCluster(t *testing.T) {
	originalPaths := constants.GetPathConst
	t.Cleanup(func() { constants.GetPathConst = originalPaths })
	root := t.TempDir()
	constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: filepath.Join(root, "conf")} }
	state := mocks.NewStateInterface(t)
	db := mocks.NewGroupedServiceQueryIntf(t)
	db.On("ExistsOnHost", context.Background(), state, "smb", "files").Return(false, nil).Once()
	db.On("GetGroupedServicesOnHost", context.Background(), state).Return([]database.GroupedService{}, nil).Once()
	originalDB := database.GroupedServicesQuery
	t.Cleanup(func() { database.GroupedServicesQuery = originalDB })
	database.GroupedServicesQuery = db
	require.NoError(t, DisableSMB(context.Background(), state, "files"))
	runtimeDir := filepath.Join(root, "samba")
	require.NoError(t, os.MkdirAll(runtimeDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "cluster-id"), []byte("other\n"), 0600))
	assert.ErrorContains(t, DisableSMB(context.Background(), state, "files"), "other")
	assert.FileExists(t, filepath.Join(runtimeDir, "cluster-id"))
}

func mockSMBPublicAddress(t *testing.T, network string, address string) {
	t.Helper()
	originalFetchConfig := fetchConfigDb
	originalNetwork := common.Network
	t.Cleanup(func() {
		fetchConfigDb = originalFetchConfig
		common.Network = originalNetwork
	})
	fetchConfigDb = func(_ context.Context, _ interfaces.StateInterface) (map[string]string, error) {
		return map[string]string{"public_network": network}, nil
	}
	networkMock := mocks.NewNetworkIntf(t)
	networkMock.On("FindIpOnSubnet", network).Return(address, nil).Once()
	common.Network = networkMock
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
