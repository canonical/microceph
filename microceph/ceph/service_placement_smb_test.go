package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

func TestGetServicePlacementTableIncludesSMB(t *testing.T) {
	placement, ok := GetServicePlacementTable()["smb"]
	require.True(t, ok)
	assert.IsType(t, &SMBServicePlacement{}, placement)
}

func TestSMBServicePlacementPayloadShapes(t *testing.T) {
	const userURI = "rados:mon-config-key:smb/config/files/users-groups.0.json"
	const direct = `{"cluster_id":"files","config_uri":"rados://.smb/files/config.smb","provider":"samba-vfs/new","user_sources":["` + userURI + `"]}`
	for _, tc := range []struct {
		name, payload string
		users         []string
	}{
		{"direct", direct, []string{userURI}},
		{"nested", `{"service_type":"smb","service_id":"files","service_name":"smb.files","placement":{"hosts":["node-a"],"count":1},"spec":` + direct + `}`, []string{userURI}},
		{"provider omitted", `{"service_type":"smb","service_id":"files","spec":` + smbTestPayload + `}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			placement := &SMBServicePlacement{}
			require.NoError(t, placement.PopulateParams(nil, tc.payload))
			assert.Equal(t, "files", placement.ClusterID)
			assert.Equal(t, "rados://.smb/files/config.smb", placement.ConfigURI)
			assert.Equal(t, tc.users, placement.UserSources)
			assert.JSONEq(t, tc.payload, string(placement.upstreamSpecJSON()))
		})
	}
}

func TestSMBServicePlacementPopulateParamsAcceptsClusteredMetadata(t *testing.T) {
	const spec = `{"service_type":"smb","service_id":"files","placement":{"hosts":["node-a","node-b"],"count":2},"spec":{
		"cluster_id":"files","config_uri":"rados://.smb/files/config.smb","features":["clustered"],
		"cluster_meta_uri":"rados://.smb/files/cluster.meta.json","cluster_lock_uri":"rados://.smb/files/cluster.meta.lock",
		"bind_addrs":[{"network":"10.0.0.0/24"}],"custom_ports":{"smb":1445}}}`
	payload := `{"service_spec":` + spec + `,"microceph":{"ctdb":{"rank":1,"identity":"smb.files.node-b"},"ctdb_ranks":{"node-a":0,"node-b":1},"next_ctdb_rank":2}}`
	placement := &SMBServicePlacement{}
	require.NoError(t, placement.PopulateParams(nil, payload))
	assert.Equal(t, []string{"clustered"}, placement.Features)
	assert.Equal(t, "rados://.smb/files/cluster.meta.json", placement.ClusterMetaURI)
	assert.Equal(t, "rados://.smb/files/cluster.meta.lock", placement.ClusterLockURI)
	require.Equal(t, &smbCTDBPlacement{Rank: 1, Identity: "smb.files.node-b"}, placement.ctdb)
	assert.Equal(t, map[string]int{"node-a": 0, "node-b": 1}, placement.ctdbRanks)
	assert.Equal(t, 2, placement.nextCTDBRank)
	assert.Equal(t, []smbBindAddress{{Network: "10.0.0.0/24"}}, placement.BindAddrs)
	assert.Equal(t, map[string]int{"smb": 1445}, placement.CustomPorts)
	assert.JSONEq(t, spec, string(placement.upstreamSpecJSON()))
}

func TestSMBServicePlacementRejectsInvalidPayload(t *testing.T) {
	for _, tc := range []struct{ name, options, message string }{
		{"missing CTDB identity", `"features":["clustered"],"cluster_meta_uri":"rados://.smb/files/cluster.meta.json","cluster_lock_uri":"rados://.smb/files/cluster.meta.lock"`, "requires CTDB node metadata"},
		{"nonclustered metadata", `"cluster_meta_uri":"rados://.smb/files/cluster.meta.json"`, "requires the clustered SMB feature"},
		{"nonclustered lock", `"cluster_lock_uri":"rados://.smb/files/cluster.meta.lock"`, "requires the clustered SMB feature"},
		{"empty bind", `"bind_addrs":[{}]`, "must set exactly one"},
		{"conflicting bind", `"bind_addrs":[{"address":"192.0.2.10","network":"192.0.2.0/24"}]`, "must set exactly one"},
		{"port range", `"custom_ports":{"smb":65536}`, "custom port smb is invalid"},
		{"unknown port", `"custom_ports":{"metrics":9922}`, "does not support custom port 'metrics'"},
		{"CTDB port", `"custom_ports":{"ctdb":14379}`, "does not support a custom CTDB port"},
		{"unsupported feature", `"features":["cephfs-proxy"]`, "does not support SMB feature 'cephfs-proxy'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := smbTestPayload[:len(smbTestPayload)-1] + "," + tc.options + "}"
			require.ErrorContains(t, (&SMBServicePlacement{}).PopulateParams(nil, payload), tc.message)
		})
	}
	payload := `{"cluster_id":"files","config_uri":"rados://.smb/other/config.smb","provider":"samba-vfs/new"}`
	require.ErrorContains(t, (&SMBServicePlacement{}).PopulateParams(nil, payload), "must use SMB cluster namespace 'files'")
}

func TestValidateSMBContainerConfigVFS(t *testing.T) {
	for _, tc := range []struct {
		name, options string
		valid         bool
	}{
		{"direct", `"vfs objects":"acl_xattr ceph_snapshots ceph_new","ceph_new:proxy":"no"`, true},
		{"proxy", `"vfs objects":"acl_xattr ceph_new","ceph_new:proxy":"yes"`, false},
		{"missing proxy setting", `"vfs objects":"acl_xattr ceph_new"`, false},
		{"classic", `"vfs objects":"acl_xattr ceph"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSMBContainerConfig([]byte(`{"shares":{"files":{"options":{` + tc.options + `}}}}`))
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "direct samba-vfs/new")
			}
		})
	}
}

func TestSMBServicePlacementHospitalityRequiresInterfaces(t *testing.T) {
	for _, clustered := range []bool{false, true} {
		t.Run(fmt.Sprint(clustered), func(t *testing.T) {
			runner := smbTestRunner(t)
			placement := &SMBServicePlacement{ClusterID: "files"}
			plug := "smb-identity"
			if clustered {
				placement.Features = []string{"clustered"}
				smbTestCommand(runner, "snapctl", "is-connected", plug).Return("", nil).Once()
				plug = "ctdb-run"
			}
			smbTestCommand(runner, "snapctl", "is-connected", plug).Return("", assert.AnError).Once()
			require.ErrorContains(t, placement.HospitalityCheck(nil), "requires the "+plug+" interface connection")
		})
	}
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
	mockSMBPublicAddress(t, "10.0.0.0/24,192.0.2.0/24", "10.0.0.12")
	address, err := resolveSMBBindAddress(context.Background(), nil, &SMBServicePlacement{})
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.12", address)
}

func TestResolveSMBBindAddressCandidates(t *testing.T) {
	for _, found := range []bool{false, true} {
		t.Run(fmt.Sprint(found), func(t *testing.T) {
			preserveSMBTestGlobal(t, &common.Network)
			network := mocks.NewNetworkIntf(t)
			common.Network = network
			network.On("FindIpOnSubnet", "198.51.100.0/24").Return("", assert.AnError).Once()
			candidate, subnet, lookupErr := "not-an-address", "", assert.AnError
			if found {
				candidate, subnet, lookupErr = "192.0.2.12", "192.0.2.0/24", nil
			}
			network.On("FindNetworkAddress", candidate).Return(subnet, lookupErr).Once()
			placement := &SMBServicePlacement{BindAddrs: []smbBindAddress{{Network: "198.51.100.0/24"}, {Address: candidate}}}
			if found {
				placement.BindAddrs = append(placement.BindAddrs, smbBindAddress{Network: "203.0.113.0/24"})
			}
			address, err := resolveSMBBindAddress(context.Background(), nil, placement)
			if found {
				require.NoError(t, err)
				assert.Equal(t, candidate, address)
			} else {
				require.ErrorContains(t, err, "failed to resolve an SMB bind address")
			}
		})
	}
}

func TestSMBServicePlacementFreshInit(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			_, conf, runtime := smbTestPaths(t, fail)
			mockSMBPublicAddress(t, "192.0.2.0/24", "192.0.2.12")
			smbTestSource(t, smbTestContainer)
			runner := smbTestRunner(t)
			const key = "[client.smb.fs.cluster.files]\\nkey = key\\n"
			smbTestCommand(runner, "ceph", "auth", "get", "client.smb.fs.cluster.files").Return(key, nil).Once()
			smbTestCommand(runner, "snapctl", "services", "microceph.smbd").Return("microceph.smbd disabled inactive", nil).Once()
			var startErr error
			if fail {
				startErr = assert.AnError
				smbTestCommand(runner, "snapctl", "stop", "microceph.smbd", "--disable").Return("", nil).Once()
			}
			smbTestCommand(runner, "snapctl", "start", "microceph.smbd", "--enable").Return("ok", startErr).Once()
			placement := &SMBServicePlacement{ClusterID: "files", ConfigURI: "rados://.smb/files/config.smb", CustomPorts: map[string]int{"smb": 1445}}
			err := placement.ServiceInit(context.Background(), nil)
			keyring := filepath.Join(conf, "ceph.client.smb.fs.cluster.files.keyring")
			config := filepath.Join(conf, "samba", "smb.conf")
			if fail {
				require.ErrorContains(t, err, "failed to start SMB service smbd")
				assert.DirExists(t, filepath.Dir(config))
				assert.NoFileExists(t, config)
				assert.NoDirExists(t, runtime)
				assert.NoFileExists(t, keyring)
			} else {
				require.NoError(t, err)
				assert.Equal(t, key, readSMBConfigFile(t, keyring))
				assert.Contains(t, readSMBConfigFile(t, config), "bind interfaces only = yes\ninterfaces = 192.0.2.12\nsmb ports = 1445\n")
			}
		})
	}
}

func TestSMBServicePlacementRejectsModeTransitionsBeforeIO(t *testing.T) {
	for _, wasClustered := range []bool{false, true} {
		t.Run(fmt.Sprint(wasClustered), func(t *testing.T) {
			_, _, runtime := smbTestPaths(t, false)
			smbTestWrite(t, filepath.Join(runtime, "cluster-id"), "files\n")
			if wasClustered {
				smbTestWrite(t, filepath.Join(runtime, "ctdb.json"), "original")
			}
			placement := &SMBServicePlacement{ClusterID: "files", ConfigURI: "rados://.smb/files/config.smb", recordedClustered: &wasClustered}
			if !wasClustered {
				placement.Features = []string{"clustered"}
			}
			require.ErrorContains(t, placement.ServiceInit(context.Background(), nil), "cannot be changed")
			require.Equal(t, "files\n", readSMBConfigFile(t, filepath.Join(runtime, "cluster-id")))
			if wasClustered {
				require.Equal(t, "original", readSMBConfigFile(t, filepath.Join(runtime, "ctdb.json")))
			}
		})
	}
}

func TestSMBServicePlacementFreshClusteredStartsCTDBBeforeSMBD(t *testing.T) {
	root, conf, runtime := smbTestPaths(t, true)
	t.Setenv("SNAP", "/snap/microceph/current")
	require.NoError(t, os.MkdirAll(runtime, 0700))
	smbTestSource(t, `{"samba-container-config":"v0","configs":{"files":{"instance_features":["ctdb"]}},"shares":{"files":{"options":{"vfs objects":"acl_xattr ceph_new","ceph_new:proxy":"no"}}}}`)
	mockSMBPublicAddress(t, "10.0.0.0/24", "10.0.0.12")
	runner := smbTestRunner(t)
	smbTestCommand(runner, "ceph", "auth", "get", "client.smb.fs.cluster.files").Return("[client.smb.fs.cluster.files]\\nkey = data\\n", nil).Once()
	smbTestCommand(runner, "ceph", "auth", "get-or-create", "client.smb.config.files", "mon", "allow r", "osd", "allow rwx pool=.smb namespace=files object_prefix cluster.meta.").Return("[client.smb.config.files]\\nkey = config\\n", nil).Once()
	var starts []*mock.Call
	for _, service := range []string{"ctdbd", "ctdb-nodes"} {
		smbTestCommand(runner, "snapctl", "services", "microceph."+service).Return("microceph."+service+" disabled inactive", nil).Once()
		starts = append(starts, smbTestCommand(runner, "snapctl", "start", "microceph."+service, "--enable").Return("ok", nil).Once())
	}
	smbTestCommand(runner, "snapctl", "services", "microceph.smbd").Return("microceph.smbd enabled active", nil).Once()
	starts = append(starts, smbTestCommand(runner, "snapctl", "restart", "microceph.smbd").Return("ok", nil).Once())
	mock.InOrder(starts...)
	placement := &SMBServicePlacement{
		ClusterID: "files", ConfigURI: "rados://.smb/files/config.smb", Features: []string{"clustered"},
		ClusterMetaURI: "rados://.smb/files/cluster.meta.json", ClusterLockURI: "rados://.smb/files/cluster.meta.lock",
		CustomPorts: map[string]int{"smb": 1445}, ctdb: &smbCTDBPlacement{Rank: 1, Identity: "smb.files.node-b"},
	}
	url := api.NewURL()
	url.Host("10.10.10.12:7443")
	state := mocks.NewStateInterface(t)
	state.On("ClusterState").Return(&mocks.MockState{URL: url, DBObj: newSMBPlacementTestDB(t)}).Twice()
	require.NoError(t, placement.ServiceInit(context.Background(), state))
	assert.Equal(t, "10.10.10.12\n", readSMBConfigFile(t, filepath.Join(runtime, "ctdb-address")))
	const bind = "bind interfaces only = yes\ninterfaces = 10.0.0.12\nsmb ports = 1445\n"
	assert.Contains(t, readSMBConfigFile(t, filepath.Join(conf, "samba", "smb.conf")), bind)
	assert.Equal(t, "[global]\nctdbd socket = /run/ctdb/ctdbd.socket\n"+bind, readSMBConfigFile(t, filepath.Join(root, "data", "samba", "smb.ctdb.conf")))
	assert.Equal(t, "[client.smb.config.files]\\nkey = config\\n", readSMBConfigFile(t, filepath.Join(conf, "ceph.client.smb.config.files.keyring")))
}

func TestStartOrRestartSMBServicesRollback(t *testing.T) {
	for _, alreadyActive := range []bool{false, true} {
		t.Run(fmt.Sprint(alreadyActive), func(t *testing.T) {
			runner := smbTestRunner(t)
			failedService := "smbd"
			if alreadyActive {
				failedService = "ctdb-nodes"
			}
			for _, service := range []string{"ctdbd", "ctdb-nodes", "smbd"} {
				status, action := "disabled inactive", "start"
				args := []string{action, "microceph." + service, "--enable"}
				if alreadyActive && service == "ctdbd" {
					status, args = "enabled active", []string{"restart", "microceph.ctdbd"}
				}
				smbTestCommand(runner, "snapctl", "services", "microceph."+service).Return("microceph."+service+" "+status, nil).Once()
				var err error
				if service == failedService {
					err = assert.AnError
				}
				smbTestCommand(runner, "snapctl", args...).Return("", err).Once()
				if err != nil {
					break
				}
			}
			if !alreadyActive {
				first := smbTestCommand(runner, "snapctl", "stop", "microceph.ctdb-nodes", "--disable").Return("", nil).Once()
				last := smbTestCommand(runner, "snapctl", "stop", "microceph.ctdbd", "--disable").Return("", nil).Once()
				mock.InOrder(first, last)
			}
			err := startOrRestartSMBServices(context.Background(), []string{"ctdbd", "ctdb-nodes", "smbd"})
			require.ErrorContains(t, err, "failed to start SMB service "+failedService)
		})
	}
}

func TestSMBServicePlacementClusteredPostCheckVerifiesAllServices(t *testing.T) {
	preserveSMBTestGlobal(t, &smbPostPlacementCheckFunc)
	checked := []string{}
	smbPostPlacementCheckFunc = func(_ context.Context, service string) error { checked = append(checked, service); return nil }
	t.Setenv("SNAP", "/snap/microceph/current")
	runner := smbTestRunner(t)
	smbTestCommand(runner, "/snap/microceph/current/commands/samba-command", "ctdb", "pnn").Return("0\n", nil).Once()
	placement := &SMBServicePlacement{Features: []string{"clustered"}, ctdb: &smbCTDBPlacement{Rank: 0}}
	require.NoError(t, placement.PostPlacementCheck(nil))
	assert.Equal(t, []string{"ctdbd", "ctdb-nodes", "smbd"}, checked)
}

func TestSMBServicePlacementDbUpdatePersistsSpecAndReceipt(t *testing.T) {
	for _, clustered := range []bool{false, true} {
		t.Run(fmt.Sprint(clustered), func(t *testing.T) {
			payload := `{"service_type":"smb","service_id":"files","service_name":"smb.files","placement":{"hosts":["node-a","node-b"]},"spec":{"cluster_id":"files","config_uri":"rados://.smb/files/config.smb","user_sources":["rados:mon-config-key:smb/config/files/users-groups.0.json"],"provider":"samba-vfs/new"}}`
			if clustered {
				payload = `{"service_spec":{"spec":{"cluster_id":"files","config_uri":"rados://.smb/files/config.smb","features":["clustered"],"cluster_meta_uri":"rados://.smb/files/cluster.meta.json","cluster_lock_uri":"rados://.smb/files/cluster.meta.lock"}},"microceph":{"ctdb":{"rank":0,"identity":"smb.files.node-a"},"ctdb_ranks":{"node-a":0,"node-b":1},"next_ctdb_rank":2}}`
			}
			placement := &SMBServicePlacement{}
			require.NoError(t, placement.PopulateParams(nil, payload))
			placement.configDigest = "successful-apply"
			state := mocks.NewStateInterface(t)
			db := smbTestGroupedDB(t)
			db.On("AddOrUpdate", context.Background(), state, "smb", "files", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				group := args.Get(4).(database.SMBServiceGroupConfig)
				info := args.Get(5).(database.SMBServiceInfo)
				assert.JSONEq(t, string(placement.upstreamSpecJSON()), string(group.DesiredSpec))
				assert.JSONEq(t, string(placement.upstreamSpecJSON()), string(info.AppliedSpec))
				assert.Equal(t, placement.configDigest, info.ConfigDigest)
				if clustered {
					assert.Equal(t, map[string]int{"node-a": 0, "node-b": 1}, group.CTDBRanks)
					assert.Equal(t, 2, group.NextCTDBRank)
					require.NotNil(t, info.CTDBRank)
					assert.Zero(t, *info.CTDBRank)
					assert.Equal(t, "smb.files.node-a", info.CTDBIdentity)
				} else {
					assert.Empty(t, group.CTDBRanks)
					assert.Zero(t, group.NextCTDBRank)
				}
			}).Return(nil).Once()
			require.NoError(t, placement.DbUpdate(context.Background(), state))
		})
	}
}

func expectSMBRetirement(t *testing.T) {
	t.Helper()
	stubSMBRecordedMode(t, nil)
	preserveSMBTestGlobal(t, &retireSMBCTDBMemberFunc)
	called := false
	t.Cleanup(func() { require.True(t, called) })
	retireSMBCTDBMemberFunc = func(ctx context.Context, clusterID string) error {
		require.NoError(t, ctx.Err())
		require.Equal(t, "files", clusterID)
		require.True(t, isSMBClusteredLocal(), "metadata must remain until retirement completes")
		called = true
		return nil
	}
}

func TestDisableSMBLocalCleanup(t *testing.T) {
	for _, scenario := range []string{"direct", "clustered auth failure", "clustered survivor"} {
		t.Run(scenario, func(t *testing.T) {
			clustered := scenario != "direct"
			if clustered {
				expectSMBRetirement(t)
			} else {
				stubSMBRecordedMode(t, nil)
			}
			root, conf, runtime := smbTestPaths(t, scenario == "clustered auth failure")
			dataKey := filepath.Join(conf, "ceph.client.smb.fs.cluster.files.keyring")
			configKey := filepath.Join(conf, "ceph.client.smb.config.files.keyring")
			include := filepath.Join(root, "data", "samba", "smb.ctdb.conf")
			require.NoError(t, os.MkdirAll(filepath.Join(conf, "samba"), 0700))
			require.NoError(t, os.MkdirAll(runtime, 0700))
			smbTestWrite(t, dataKey, "keyring")
			services := []string{"smbd"}
			if clustered {
				smbTestWrite(t, filepath.Join(runtime, "ctdb.json"), "{}")
				services = append(services, "ctdb-nodes", "ctdbd")
			}
			if scenario == "clustered auth failure" {
				smbTestWrite(t, configKey, "keyring")
				smbTestWrite(t, include, "stale")
			}
			state := mocks.NewStateInterface(t)
			db := smbTestGroupedDB(t)
			db.On("ExistsOnHost", context.Background(), state, "smb", "files").Return(true, nil).Once()
			db.On("RemoveForHost", context.Background(), state, "smb", "files").Return(nil).Once()
			if clustered {
				remaining := []database.GroupedService{}
				if scenario == "clustered survivor" {
					remaining = append(remaining, database.GroupedService{Service: "smb", GroupID: "files", Member: "node-b"})
				}
				db.On("GetGroupedServices", context.Background(), state).Return(remaining, nil).Once()
			}
			runner := smbTestRunner(t)
			for _, service := range services {
				smbTestCommand(runner, "snapctl", "stop", "microceph."+service, "--disable").Return("", nil).Once()
			}
			if scenario == "clustered auth failure" {
				smbTestCommand(runner, "ceph", "auth", "del", "client.smb.config.files").Return("", assert.AnError).Once()
			}
			require.NoError(t, DisableSMB(context.Background(), state, "files"))
			assert.DirExists(t, filepath.Join(conf, "samba"))
			assert.NoFileExists(t, filepath.Join(conf, "samba", "smb.conf"))
			assert.NoDirExists(t, runtime)
			assert.NoFileExists(t, dataKey)
			if scenario == "clustered auth failure" {
				assert.NoFileExists(t, configKey)
				assert.NoFileExists(t, include)
			}
		})
	}
}

func TestSMBPlacementReceiptSkipsOnlyCurrentEffectiveConfig(t *testing.T) {
	_, conf, runtime := smbTestPaths(t, false)
	for path, content := range map[string]string{
		filepath.Join(runtime, "cluster-id"):                            "files\n",
		filepath.Join(conf, "samba", "smb.conf"):                        "config",
		filepath.Join(conf, "ceph.client.smb.fs.cluster.files.keyring"): "key",
	} {
		smbTestWrite(t, path, content)
	}
	mockSMBPublicAddress(t, "192.0.2.0/24", "192.0.2.12")
	// The same URI can refer to changed shares or credentials.
	container, user := []byte(smbTestContainer), []byte(`{"users":["alice"]}`)
	preserveSMBTestGlobal(t, &fetchSMBSourceFunc)
	fetchSMBSourceFunc = func(_ context.Context, uri string) ([]byte, error) {
		if uri == "rados://.smb/files/config.smb" {
			return container, nil
		}
		return user, nil
	}
	runner := smbTestRunner(t)
	smbTestCommand(runner, "snapctl", "services", "microceph.smbd").Return("microceph.smbd enabled active", nil).Twice()
	payload := `{"cluster_id":"files","config_uri":"rados://.smb/files/config.smb","user_sources":["rados:mon-config-key:smb/config/files/users-groups.0.json"]}`
	placement := &SMBServicePlacement{}
	require.NoError(t, placement.PopulateParams(nil, payload))
	placement.bindAddress = "192.0.2.12"
	data, err := fetchSMBConfigSources(context.Background(), placement)
	require.NoError(t, err)
	placement.configData = data
	smbTestWrite(t, filepath.Join(runtime, "container.json"), string(container))
	smbTestWrite(t, filepath.Join(runtime, "users-0.json"), string(user))
	digest, err := placement.effectiveConfigDigest()
	require.NoError(t, err)
	info, err := json.Marshal(database.SMBServiceInfo{AppliedSpec: placement.upstreamSpec, ConfigDigest: digest})
	require.NoError(t, err)
	state := mocks.NewStateInterface(t)
	state.On("ClusterState").Return(&mocks.MockState{DBObj: newSMBPlacementTestDB(t)}).Once()
	db := smbTestGroupedDB(t)
	db.On("GetGroupedServicesOnHost", context.Background(), state).Return([]database.GroupedService{{Service: "smb", GroupID: "files", Info: string(info)}}, nil).Times(5)
	fresh := &SMBServicePlacement{}
	require.NoError(t, fresh.PopulateParams(nil, payload))
	require.NoError(t, fresh.ServiceInit(context.Background(), state))
	assert.True(t, fresh.unchanged)
	preserveSMBTestGlobal(t, &smbPostPlacementCheckFunc)
	smbPostPlacementCheckFunc = func(_ context.Context, service string) error { require.Equal(t, "smbd", service); return nil }
	require.NoError(t, fresh.PostPlacementCheck(state))
	db.On("AddOrUpdate", context.Background(), state, "smb", "files", mock.Anything, mock.Anything).Return(nil).Once()
	require.NoError(t, fresh.DbUpdate(context.Background(), state))

	changed := &SMBServicePlacement{}
	require.NoError(t, changed.PopulateParams(nil, payload))
	changed.bindAddress = "192.0.2.12"
	for _, change := range []string{"placement", "credentials", "shares"} {
		if change == "placement" {
			var spec map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(changed.upstreamSpec, &spec))
			spec["placement"] = json.RawMessage(`{"hosts":["node-a","node-b"],"count":1}`)
			changed.upstreamSpec, err = json.Marshal(spec)
			require.NoError(t, err)
		} else if change == "credentials" {
			user = []byte(`{"users":["bob"]}`)
		} else {
			user = []byte(`{"users":["alice"]}`)
			container = []byte(`{"shares":{"new-share":{"options":{"vfs objects":"ceph_new","ceph_new:proxy":"no"}}}}`)
		}
		changed.configData, err = fetchSMBConfigSources(context.Background(), changed)
		require.NoError(t, err)
		changedDigest, err := changed.effectiveConfigDigest()
		require.NoError(t, err)
		changed.configDigest = changedDigest
		matched, err := changed.matchesAppliedReceipt(context.Background(), state)
		require.NoError(t, err)
		if change == "placement" {
			assert.Equal(t, digest, changedDigest, change)
			assert.True(t, matched, change)
		} else {
			assert.NotEqual(t, digest, changedDigest, change)
			assert.False(t, matched, change)
		}
	}
	// A receipt does not mask a missing node-local file.
	require.NoError(t, os.Remove(filepath.Join(conf, "samba", "smb.conf")))
	matched, err := fresh.matchesAppliedReceipt(context.Background(), state)
	require.NoError(t, err)
	assert.False(t, matched)
}

func TestSMBPartialUpdateRestoresPreviousFiles(t *testing.T) {
	_, conf, runtime := smbTestPaths(t, false)
	for path, content := range map[string]string{
		filepath.Join(runtime, "cluster-id"):     "files\n",
		filepath.Join(runtime, "container.json"): "old container",
		filepath.Join(conf, "samba", "smb.conf"): "old smb config",
	} {
		smbTestWrite(t, path, content)
	}
	mockSMBPublicAddress(t, "192.0.2.0/24", "192.0.2.12")
	smbTestSource(t, smbTestContainer)
	preserveSMBTestGlobal(t, &writeSMBFileFunc)
	failed := false
	writeSMBFileFunc = func(path string, data []byte, mode os.FileMode) error {
		if path == filepath.Join(runtime, "container.json.tmp") && !failed {
			failed = true
			return assert.AnError
		}
		return os.WriteFile(path, data, mode)
	}
	runner := smbTestRunner(t)
	smbTestCommand(runner, "snapctl", "services", "microceph.smbd").Return("active", nil).Once()
	smbTestCommand(runner, "snapctl", "restart", "microceph.smbd").Return("ok", nil).Once()
	state := mocks.NewStateInterface(t)
	state.On("ClusterState").Return(&mocks.MockState{DBObj: newSMBPlacementTestDB(t)}).Once()
	db := smbTestGroupedDB(t)
	db.On("GetGroupedServicesOnHost", context.Background(), state).Return([]database.GroupedService{}, nil).Once()
	placement := &SMBServicePlacement{ClusterID: "files", ConfigURI: "rados://.smb/files/config.smb"}
	require.ErrorContains(t, placement.ServiceInit(context.Background(), state), "failed to write SMB container configuration")
	assert.Equal(t, "old smb config", readSMBConfigFile(t, filepath.Join(conf, "samba", "smb.conf")))
	assert.Equal(t, "old container", readSMBConfigFile(t, filepath.Join(runtime, "container.json")))
}

func TestSMBPlacementLegacyReceiptNeverSkips(t *testing.T) {
	placement := &SMBServicePlacement{ClusterID: "files", configDigest: "digest"}
	state := mocks.NewStateInterface(t)
	db := smbTestGroupedDB(t)
	db.On("GetGroupedServicesOnHost", context.Background(), state).Return([]database.GroupedService{{Service: "smb", GroupID: "files", Info: `{"config_uri":"rados://.smb/files/config.smb"}`}}, nil).Once()
	matched, err := placement.matchesAppliedReceipt(context.Background(), state)
	require.NoError(t, err)
	assert.False(t, matched)
}

func TestDisableSMBCleansMatchingLocalStateWithoutMemberRecord(t *testing.T) {
	stubSMBRecordedMode(t, nil)
	_, _, runtime := smbTestPaths(t, false)
	smbTestWrite(t, filepath.Join(runtime, "cluster-id"), "files\n")
	state := mocks.NewStateInterface(t)
	db := smbTestGroupedDB(t)
	db.On("ExistsOnHost", context.Background(), state, "smb", "files").Return(false, nil).Twice()
	db.On("GetGroupedServicesOnHost", context.Background(), state).Return([]database.GroupedService{}, nil).Twice()
	runner := smbTestRunner(t)
	smbTestCommand(runner, "snapctl", "stop", "microceph.smbd", "--disable").Return("", nil).Twice()
	require.NoError(t, DisableSMB(context.Background(), state, "files"))
	require.NoDirExists(t, runtime)

	// Interrupted clustered cleanup can leave the marker and CTDB configuration
	// after its retirement inputs and receipt are already gone.
	smbTestWrite(t, filepath.Join(runtime, "cluster-id"), "files\n")
	smbTestWrite(t, filepath.Join(runtime, "ctdb.json"), "{}")
	db.On("GetGroupedServices", context.Background(), state).Return([]database.GroupedService{}, nil).Once()
	for _, service := range []string{"ctdb-nodes", "ctdbd"} {
		smbTestCommand(runner, "snapctl", "stop", "microceph."+service, "--disable").Return("", nil).Once()
	}
	smbTestCommand(runner, "ceph", "auth", "del", "client.smb.config.files").Return("", nil).Once()
	require.NoError(t, DisableSMB(context.Background(), state, "files"))
	require.NoDirExists(t, runtime)
}

func TestDisableSMBAbsentAndWrongCluster(t *testing.T) {
	_, _, runtime := smbTestPaths(t, false)
	state := mocks.NewStateInterface(t)
	db := smbTestGroupedDB(t)
	db.On("ExistsOnHost", context.Background(), state, "smb", "files").Return(false, nil).Once()
	db.On("GetGroupedServicesOnHost", context.Background(), state).Return([]database.GroupedService{}, nil).Once()
	require.NoError(t, DisableSMB(context.Background(), state, "files"))
	smbTestWrite(t, filepath.Join(runtime, "cluster-id"), "other\n")
	assert.ErrorContains(t, DisableSMB(context.Background(), state, "files"), "other")
	assert.FileExists(t, filepath.Join(runtime, "cluster-id"))
}

func mockSMBPublicAddress(t *testing.T, network, address string) {
	t.Helper()
	preserveSMBTestGlobal(t, &fetchConfigDb)
	preserveSMBTestGlobal(t, &common.Network)
	fetchConfigDb = func(context.Context, interfaces.StateInterface) (map[string]string, error) {
		return map[string]string{"public_network": network}, nil
	}
	stub := mocks.NewNetworkIntf(t)
	stub.On("FindIpOnSubnet", network).Return(address, nil).Once()
	common.Network = stub
}
