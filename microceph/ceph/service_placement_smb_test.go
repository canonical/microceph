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
			}
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
	runner.On("RunCommand", "snapctl", "is-connected", "smb-identity").Return("", nil).Once()
	runner.On("RunCommand", "snapctl", "is-connected", "ctdb-run").Return("", assert.AnError).Once()
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
	baseConfig := readSMBConfigFile(t, filepath.Join(confPath, "samba", "smb.conf"))
	assert.Contains(t, baseConfig, "bind interfaces only = yes\ninterfaces = 192.0.2.12\n")
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
	fetchSMBSourceFunc = func(_ string) ([]byte, error) {
		return []byte(`{"shares":{"files":{"options":{"vfs objects":"ceph_new","ceph_new:proxy":"no"}}}}`), nil
	}

	runner := mocks.NewRunner(t)
	runner.On("RunCommand", "ceph", "auth", "get", "client.smb.fs.cluster.files").
		Return("key", nil).Once()
	runner.On("RunCommand", "snapctl", "services", "microceph.smbd").
		Return("microceph.smbd disabled inactive", nil).Once()
	runner.On("RunCommand", "snapctl", "start", "microceph.smbd", "--enable").
		Return("", assert.AnError).Once()
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

func TestSMBServicePlacementDirectServiceInitStopsStaleCTDB(t *testing.T) {
	tempDir := t.TempDir()
	confPath := filepath.Join(tempDir, "conf")
	dataPath := filepath.Join(tempDir, "data")
	runtimePath := filepath.Join(tempDir, "samba")
	mockSMBPublicAddress(t, "192.0.2.0/24", "192.0.2.12")

	originalPaths := constants.GetPathConst
	defer func() {
		constants.GetPathConst = originalPaths
	}()
	constants.GetPathConst = func() constants.PathConst {
		return constants.PathConst{ConfPath: confPath, DataPath: dataPath}
	}
	err := os.MkdirAll(runtimePath, 0700)
	require.NoError(t, err)
	for _, name := range []string{"ctdb.json", "ctdb-rank", "ctdb-identity", "ctdb-address"} {
		err = os.WriteFile(filepath.Join(runtimePath, name), []byte("stale"), 0600)
		require.NoError(t, err)
	}
	err = os.WriteFile(filepath.Join(runtimePath, "cluster-id"), []byte("files\n"), 0600)
	require.NoError(t, err)
	ctdbIncludePath := filepath.Join(dataPath, "samba", "smb.ctdb.conf")
	err = os.MkdirAll(filepath.Dir(ctdbIncludePath), 0700)
	require.NoError(t, err)
	err = os.WriteFile(ctdbIncludePath, []byte("stale"), 0600)
	require.NoError(t, err)

	originalFetch := fetchSMBSourceFunc
	defer func() {
		fetchSMBSourceFunc = originalFetch
	}()
	fetchSMBSourceFunc = func(_ string) ([]byte, error) {
		return []byte(`{"shares":{"files":{"options":{"vfs objects":"ceph_new","ceph_new:proxy":"no"}}}}`), nil
	}

	runner := mocks.NewRunner(t)
	runner.On("RunCommand", "ceph", "auth", "get", "client.smb.fs.cluster.files").Return("key", nil).Once()
	runner.On("RunCommand", "snapctl", "stop", "microceph.ctdb-nodes", "--disable").Return("ok", nil).Once()
	runner.On("RunCommand", "snapctl", "stop", "microceph.ctdbd", "--disable").Return("ok", nil).Once()
	runner.On("RunCommand", "snapctl", "services", "microceph.smbd").Return("microceph.smbd enabled active", nil).Once()
	runner.On("RunCommand", "snapctl", "restart", "microceph.smbd").Return("ok", nil).Once()
	originalRunner := common.ProcessExec
	defer func() {
		common.ProcessExec = originalRunner
	}()
	common.ProcessExec = runner

	placement := &SMBServicePlacement{
		ClusterID: "files",
		ConfigURI: "rados://.smb/files/config.smb",
	}
	err = placement.ServiceInit(context.Background(), nil)

	require.NoError(t, err)
	for _, name := range []string{"ctdb.json", "ctdb-rank", "ctdb-identity", "ctdb-address"} {
		assert.NoFileExists(t, filepath.Join(runtimePath, name))
	}
	assert.NoFileExists(t, ctdbIncludePath)
}

func TestSMBServicePlacementDirectToClusteredStartsCTDBBeforeRestartingSMBD(t *testing.T) {
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
	err = os.WriteFile(filepath.Join(runtimePath, "cluster-id"), []byte("files\n"), 0600)
	require.NoError(t, err)

	originalFetch := fetchSMBSourceFunc
	defer func() {
		fetchSMBSourceFunc = originalFetch
	}()
	fetchSMBSourceFunc = func(_ string) ([]byte, error) {
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
	runner.On("RunCommand", "ceph", "auth", "get", "client.smb.fs.cluster.files").Return("[client.smb.fs.cluster.files]\\nkey = data\\n", nil).Once()
	runner.On(
		"RunCommand",
		"ceph",
		"auth",
		"get-or-create",
		"client.smb.config.files",
		"mon",
		"allow r",
		"osd",
		"allow rwx pool=.smb namespace=files object_prefix cluster.meta.",
	).Return("[client.smb.config.files]\\nkey = config\\n", nil).Once()
	for _, service := range []string{"ctdbd", "ctdb-nodes"} {
		runner.On("RunCommand", "snapctl", "services", "microceph."+service).Return("microceph."+service+" disabled inactive", nil).Once()
		runner.On("RunCommand", "snapctl", "start", "microceph."+service, "--enable").Return("ok", nil).Once()
	}
	runner.On("RunCommand", "snapctl", "services", "microceph.smbd").Return("microceph.smbd enabled active", nil).Once()
	runner.On("RunCommand", "snapctl", "restart", "microceph.smbd").Return("ok", nil).Once()
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
	assert.Contains(t, baseConfig, "bind interfaces only = yes\ninterfaces = 10.0.0.12\n")
	ctdbConfig := readSMBConfigFile(t, filepath.Join(tempDir, "data", "samba", "smb.ctdb.conf"))
	assert.Contains(t, ctdbConfig, "bind interfaces only = yes\ninterfaces = 10.0.0.12\n")
	configKeyring, err := os.ReadFile(filepath.Join(confPath, "ceph.client.smb.config.files.keyring"))
	require.NoError(t, err)
	assert.Equal(t, "[client.smb.config.files]\\nkey = config\\n", string(configKeyring))
}

func TestStartOrRestartSMBServicesRollsBackNewServices(t *testing.T) {
	runner := mocks.NewRunner(t)
	for _, service := range []string{"ctdbd", "ctdb-nodes", "smbd"} {
		runner.On("RunCommand", "snapctl", "services", "microceph."+service).
			Return("microceph."+service+" disabled inactive", nil).Once()
		if service == "smbd" {
			runner.On("RunCommand", "snapctl", "start", "microceph.smbd", "--enable").
				Return("", assert.AnError).Once()
			continue
		}
		runner.On("RunCommand", "snapctl", "start", "microceph."+service, "--enable").
			Return("ok", nil).Once()
	}
	runner.On("RunCommand", "snapctl", "stop", "microceph.ctdb-nodes", "--disable").
		Return("ok", nil).Once()
	runner.On("RunCommand", "snapctl", "stop", "microceph.ctdbd", "--disable").
		Return("ok", nil).Once()
	originalRunner := common.ProcessExec
	defer func() {
		common.ProcessExec = originalRunner
	}()
	common.ProcessExec = runner

	err := startOrRestartSMBServices([]string{"ctdbd", "ctdb-nodes", "smbd"})

	assert.ErrorContains(t, err, "failed to start SMB service smbd")
}

func TestStartOrRestartSMBServicesKeepsPreviouslyActiveServicesOnFailure(t *testing.T) {
	runner := mocks.NewRunner(t)
	runner.On("RunCommand", "snapctl", "services", "microceph.ctdbd").
		Return("microceph.ctdbd enabled active", nil).Once()
	runner.On("RunCommand", "snapctl", "restart", "microceph.ctdbd").
		Return("ok", nil).Once()
	runner.On("RunCommand", "snapctl", "services", "microceph.ctdb-nodes").
		Return("microceph.ctdb-nodes disabled inactive", nil).Once()
	runner.On("RunCommand", "snapctl", "start", "microceph.ctdb-nodes", "--enable").
		Return("", assert.AnError).Once()
	originalRunner := common.ProcessExec
	defer func() {
		common.ProcessExec = originalRunner
	}()
	common.ProcessExec = runner

	err := startOrRestartSMBServices([]string{"ctdbd", "ctdb-nodes", "smbd"})

	assert.ErrorContains(t, err, "failed to start SMB service ctdb-nodes")
}

func TestSMBServicePlacementClusteredPostCheckVerifiesAllServices(t *testing.T) {
	originalCheck := smbPostPlacementCheckFunc
	defer func() {
		smbPostPlacementCheckFunc = originalCheck
	}()
	checked := []string{}
	smbPostPlacementCheckFunc = func(service string) error {
		checked = append(checked, service)
		return nil
	}
	placement := &SMBServicePlacement{Features: []string{"clustered"}}

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
		database.SMBServiceInfo{ConfigURI: "rados://.smb/files/config.smb"},
	).Run(func(args mock.Arguments) {
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

	err = placement.DbUpdate(ctx, state)

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

func TestDisableSMBStopsClusteredServicesAndRemovesConfigKeyring(t *testing.T) {
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
		runner.On("RunCommand", "snapctl", "stop", "microceph."+service, "--disable").Return("ok", nil).Once()
	}
	runner.On("RunCommand", "ceph", "auth", "del", "client.smb.config.files").Return("", nil).Once()
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
		runner.On("RunCommand", "snapctl", "stop", "microceph."+service, "--disable").
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
