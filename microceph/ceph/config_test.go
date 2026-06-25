package ceph

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
)

type configSuite struct {
	tests.BaseSuite
}

func TestConfig(t *testing.T) {
	suite.Run(t, new(configSuite))
}

// Set up test suite
func (s *configSuite) SetupTest() {
	s.BaseSuite.SetupTest()
}

func addConfigSetExpectations(r *mocks.Runner, key string, value string) {
	r.On("RunCommand", []interface{}{
		"ceph", "config", "set", "global", key, value, "-f", "json-pretty",
	}...).Return(value, nil).Once()
}

func addConfigOpExpectations(r *mocks.Runner, op, who, key, value string) {
	r.On("RunCommand", []interface{}{
		"ceph", "config", op, who, key,
	}...).Return(value, nil).Once()
}

func addListConfigExpectations(r *mocks.Runner, key string, value string) {
	var configs = ConfigDump{}
	configs = append(configs, ConfigDumpItem{Section: "", Name: key, Value: value})
	ret, _ := json.Marshal(configs)
	r.On("RunCommand", []interface{}{
		"ceph", "config", "dump", "-f", "json-pretty",
	}...).Return(string(ret[:]), nil).Once()
}

func (s *configSuite) TestSetConfig() {
	t := types.Config{Key: "cluster_network", Value: "0.0.0.0/16"}

	r := mocks.NewRunner(s.T())
	addConfigSetExpectations(r, t.Key, t.Value)
	common.ProcessExec = r

	err := SetConfigItem(t)
	assert.NoError(s.T(), err)
}

func (s *configSuite) TestSetROConfig() {
	t := types.Config{Key: "public_network", Value: "0.0.0.0/16"}

	err := SetConfigItem(t)
	assert.ErrorContains(s.T(), err, "does not support write operation")
}

func (s *configSuite) TestSetROConfigBypassChecks() {
	t := types.Config{Key: "public_network", Value: "0.0.0.0/16"}

	r := mocks.NewRunner(s.T())
	addConfigSetExpectations(r, t.Key, t.Value)
	common.ProcessExec = r

	err := SetConfigItemUnsafe(t)
	assert.NoError(s.T(), err)
}

func (s *configSuite) TestSetUnknowConfig() {
	t := types.Config{Key: "unknown_config", Value: "0.0.0.0/16"}

	err := SetConfigItem(t)
	assert.ErrorContains(s.T(), err, "is not a MicroCeph supported cluster config")
}

func (s *configSuite) TestGetConfig() {
	t := types.Config{Key: "cluster_network", Value: "0.0.0.0/16"}

	r := mocks.NewRunner(s.T())
	addConfigOpExpectations(r, "get", "mon", t.Key, t.Value)
	common.ProcessExec = r

	_, err := GetConfigItem(t)
	assert.NoError(s.T(), err)
}

func (s *configSuite) TestGetUnknownConfig() {
	t := types.Config{Key: "unknown_config", Value: "0.0.0.0/16"}

	_, err := GetConfigItem(t)
	assert.ErrorContains(s.T(), err, "is not a MicroCeph supported cluster config")
}

func (s *configSuite) TestResetConfig() {
	t := types.Config{Key: "cluster_network", Value: "0.0.0.0/16"}

	r := mocks.NewRunner(s.T())
	addConfigOpExpectations(r, "rm", "global", t.Key, t.Value)
	common.ProcessExec = r

	err := RemoveConfigItem(t)
	assert.NoError(s.T(), err)
}

func (s *configSuite) TestListConfig() {
	t := types.Config{Key: "cluster_network", Value: "0.0.0.0/16"}

	r := mocks.NewRunner(s.T())
	addListConfigExpectations(r, t.Key, t.Value)
	common.ProcessExec = r

	configs, err := ListConfigs()
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), configs[0].Key, t.Key)
	assert.Equal(s.T(), configs[0].Value, t.Value)
}

// --- UpdateConfig / radosgw.conf mon host refresh tests (issue #766) ---

// setupUpdateConfigMocks wires the injectable seams that UpdateConfig relies on
// (the public_network backward-compat shim, the config DB fetch, the network
// lookup and the client-config query) so it can run end-to-end without a real
// database or network. The supplied configMap is returned verbatim by the
// fetch override; mon.host.<n> entries in it become the monitor list.
func (s *configSuite) setupUpdateConfigMocks(configMap map[string]string) *mocks.StateInterface {
	// Short-circuit the public_network backward-compat shim so it neither
	// queries the database nor calls FindNetworkAddress.
	origGMC := getMonitorCountFunc
	s.T().Cleanup(func() { getMonitorCountFunc = origGMC })
	getMonitorCountFunc = func(_ context.Context, _ interfaces.StateInterface) (int, error) {
		return 0, nil
	}

	// Inject the config map without touching the database.
	origFetch := fetchConfigDb
	s.T().Cleanup(func() { fetchConfigDb = origFetch })
	fetchConfigDb = func(_ context.Context, _ interfaces.StateInterface) (map[string]string, error) {
		return configMap, nil
	}

	// Network: claim the host has an IP on the configured public network.
	origNet := common.Network
	s.T().Cleanup(func() { common.Network = origNet })
	net := mocks.NewNetworkIntf(s.T())
	net.On("FindIpOnSubnet", configMap["public_network"]).Return("10.0.0.5", nil).Maybe()
	common.Network = net

	// Client config query: no per-host client configs.
	origCCQ := database.ClientConfigQuery
	s.T().Cleanup(func() { database.ClientConfigQuery = origCCQ })
	ccq := mocks.NewClientConfigQueryIntf(s.T())
	ccq.On("GetAllForHost", mock.Anything, mock.Anything).Return(database.ClientConfigItems{}, nil).Maybe()
	database.ClientConfigQuery = ccq

	return s.newMockState()
}

// monHostsFromConf extracts the comma-separated monitors from the first
// `mon host =` line of a rendered config file.
func monHostsFromConf(t *testing.T, conf string) []string {
	t.Helper()
	parts := strings.SplitN(conf, "mon host = ", 2)
	assert.Len(t, parts, 2, "conf must contain a mon host line")
	rest := strings.SplitN(parts[1], "\n", 2)[0]
	hosts := strings.Split(rest, ",")
	for i := range hosts {
		hosts[i] = strings.TrimSpace(hosts[i])
	}
	return hosts
}

// TestUpdateConfigRefreshesRadosGWMonHost is the reproducer and regression test
// for issue #766: radosgw.conf's `mon host` line is written once at RGW enable
// time and never refreshed, so it goes permanently stale as monitors join the
// cluster. The per-minute refresh path (UpdateConfig) that re-renders ceph.conf
// must also keep radosgw.conf's mon host in sync, rewriting the line in place so
// the unpersisted RGW frontend/port/SSL settings are preserved.
func (s *configSuite) TestUpdateConfigRefreshesRadosGWMonHost() {
	s.CopyCephConfigs()

	// Seed radosgw.conf with a STALE mon host list: only 2 of the 3 current
	// monitors, as a later-joined node would have recorded at enable time.
	stale := `# Generated by MicroCeph, DO NOT EDIT.
[global]
mon host = 192.168.123.11,192.168.123.12
run dir = /var/snap/microceph/current/run
auth allow insecure global id reclaim = false

[client.radosgw.gateway]
rgw init timeout = 1200
rgw frontends = beast port=80
`
	rgwPath := filepath.Join(s.Tmp, "SNAP_DATA", "conf", "radosgw.conf")
	err := os.WriteFile(rgwPath, []byte(stale), 0644)
	assert.NoError(s.T(), err)

	// The cluster now knows 3 monitors; node 3 (192.168.123.13) joined after
	// RGW was enabled.
	configMap := map[string]string{
		"fsid":                 "test-fsid",
		"public_network":       "10.0.0.0/24",
		"keyring.client.admin": "test-admin-key",
		"mon.host.1":           "192.168.123.11",
		"mon.host.2":           "192.168.123.12",
		"mon.host.3":           "192.168.123.13",
	}
	state := s.setupUpdateConfigMocks(configMap)

	err = UpdateConfig(context.Background(), state)
	assert.NoError(s.T(), err)

	// ceph.conf must reflect all 3 monitors — sanity that the refresh ran.
	cephHosts := monHostsFromConf(s.T(), s.ReadCephConfig("ceph.conf"))
	assert.Len(s.T(), cephHosts, 3)
	assert.Contains(s.T(), cephHosts, "192.168.123.13")

	// radosgw.conf's mon host must now be in sync: the previously-missing 3rd
	// monitor is present and the list has grown from 2 to 3.
	rgwHosts := monHostsFromConf(s.T(), s.ReadCephConfig("radosgw.conf"))
	assert.Len(s.T(), rgwHosts, 3)
	assert.Contains(s.T(), rgwHosts, "192.168.123.13")

	// The in-place rewrite must preserve the unpersisted RGW frontend settings.
	rgwConf := s.ReadCephConfig("radosgw.conf")
	assert.Contains(s.T(), rgwConf, "rgw frontends = beast port=80")
	assert.Contains(s.T(), rgwConf, "rgw init timeout = 1200")
}

// TestUpdateConfigNoRGWConfIsNoOp verifies that when RGW is not enabled (no
// radosgw.conf), UpdateConfig still succeeds and does not create the file.
func (s *configSuite) TestUpdateConfigNoRGWConfIsNoOp() {
	s.CopyCephConfigs()

	configMap := map[string]string{
		"fsid":                 "test-fsid",
		"public_network":       "10.0.0.0/24",
		"keyring.client.admin": "test-admin-key",
		"mon.host.1":           "192.168.123.11",
		"mon.host.2":           "192.168.123.12",
	}
	state := s.setupUpdateConfigMocks(configMap)

	err := UpdateConfig(context.Background(), state)
	assert.NoError(s.T(), err)

	// radosgw.conf must not have been created.
	_, statErr := os.Stat(filepath.Join(s.Tmp, "SNAP_DATA", "conf", "radosgw.conf"))
	assert.True(s.T(), os.IsNotExist(statErr))
}

// --- backwardCompatPubnet gate tests ---

// overrideGetMonitorCount replaces getMonitorCountFunc for the duration of a test
// and restores the original on cleanup.
func (s *configSuite) overrideGetMonitorCount(count int, err error) {
	orig := getMonitorCountFunc
	s.T().Cleanup(func() { getMonitorCountFunc = orig })
	getMonitorCountFunc = func(_ context.Context, _ interfaces.StateInterface) (int, error) {
		return count, err
	}
}

// newMockState returns a StateInterface mock backed by a MockState with an
// empty URL (Hostname returns ""), sufficient for tests that only need
// s.ClusterState().Address().Hostname().
func (s *configSuite) newMockState() *mocks.StateInterface {
	si := mocks.NewStateInterface(s.T())
	si.On("ClusterState").Return(&mocks.MockState{URL: api.NewURL()}).Maybe()
	return si
}

// TestBackwardCompatPubnetNoMonitors verifies that the shim returns nil without
// touching the config when no monitors are registered (pre-bootstrap state).
func (s *configSuite) TestBackwardCompatPubnetNoMonitors() {
	s.overrideGetMonitorCount(0, nil)

	// fetchConfigDb should never be called — we return before reaching it
	origFetch := fetchConfigDb
	s.T().Cleanup(func() { fetchConfigDb = origFetch })
	fetchCalled := false
	fetchConfigDb = func(_ context.Context, _ interfaces.StateInterface) (map[string]string, error) {
		fetchCalled = true
		return nil, nil
	}

	err := backwardCompatPubnet(context.Background(), nil)
	assert.NoError(s.T(), err)
	assert.False(s.T(), fetchCalled, "fetchConfigDb should not be called when no monitors are registered")
}

// TestBackwardCompatPubnetMonitorQueryFails verifies that a database error from
// the monitor gate is propagated to the caller.
func (s *configSuite) TestBackwardCompatPubnetMonitorQueryFails() {
	dbErr := errors.New("db unavailable")
	s.overrideGetMonitorCount(0, dbErr)

	err := backwardCompatPubnet(context.Background(), nil)
	assert.ErrorContains(s.T(), err, "failed to check for registered monitors")
	assert.ErrorIs(s.T(), err, dbErr)
}

// TestBackwardCompatPubnetMonitorsPresentPublicNetSet verifies that the shim
// returns nil without calling FindNetworkAddress when monitors exist and
// public_network is already a valid CIDR.
func (s *configSuite) TestBackwardCompatPubnetMonitorsPresentPublicNetSet() {
	s.overrideGetMonitorCount(1, nil)

	// Stub GetConfigDb to return a pre-set public_network.
	origFetch := fetchConfigDb
	s.T().Cleanup(func() { fetchConfigDb = origFetch })
	fetchConfigDb = func(_ context.Context, _ interfaces.StateInterface) (map[string]string, error) {
		return map[string]string{"public_network": "10.0.0.0/24"}, nil
	}

	net := mocks.NewNetworkIntf(s.T())
	common.Network = net

	err := backwardCompatPubnet(context.Background(), nil)
	assert.NoError(s.T(), err)
	net.AssertNotCalled(s.T(), "FindNetworkAddress")
}

// TestBackwardCompatPubnetMonitorsPresentPublicNetMissing verifies that the
// shim calls FindNetworkAddress and writes the result when monitors exist but
// public_network is absent from the config.
func (s *configSuite) TestBackwardCompatPubnetMonitorsPresentPublicNetMissing() {
	s.overrideGetMonitorCount(1, nil)

	origFetch := fetchConfigDb
	s.T().Cleanup(func() { fetchConfigDb = origFetch })
	fetchConfigDb = func(_ context.Context, _ interfaces.StateInterface) (map[string]string, error) {
		return map[string]string{}, nil
	}

	origInsert := insertPubnetRecord
	s.T().Cleanup(func() { insertPubnetRecord = origInsert })
	var insertedNet string
	insertPubnetRecord = func(_ context.Context, _ interfaces.StateInterface, pubNet string) error {
		insertedNet = pubNet
		return nil
	}

	net := mocks.NewNetworkIntf(s.T())
	net.On("FindNetworkAddress", "").Return("192.168.1.0/24", nil).Once()
	common.Network = net

	err := backwardCompatPubnet(context.Background(), s.newMockState())
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "192.168.1.0/24", insertedNet)
}

// TestBackwardCompatPubnetInsertFails verifies that an error from the database
// write is propagated — regression for the previous silent-drop bug.
func (s *configSuite) TestBackwardCompatPubnetInsertFails() {
	s.overrideGetMonitorCount(1, nil)

	origFetch := fetchConfigDb
	s.T().Cleanup(func() { fetchConfigDb = origFetch })
	fetchConfigDb = func(_ context.Context, _ interfaces.StateInterface) (map[string]string, error) {
		return map[string]string{}, nil
	}

	dbErr := errors.New("constraint violation")
	origInsert := insertPubnetRecord
	s.T().Cleanup(func() { insertPubnetRecord = origInsert })
	insertPubnetRecord = func(_ context.Context, _ interfaces.StateInterface, _ string) error {
		return dbErr
	}

	net := mocks.NewNetworkIntf(s.T())
	net.On("FindNetworkAddress", "").Return("192.168.1.0/24", nil).Once()
	common.Network = net

	err := backwardCompatPubnet(context.Background(), s.newMockState())
	assert.ErrorIs(s.T(), err, dbErr)
}

// TestBackwardCompatPubnetMonitorsPresentPublicNetMultiSubnet verifies that the
// shim treats a stored comma-delimited multi-subnet public_network as valid
// and does not re-detect / overwrite.
func (s *configSuite) TestBackwardCompatPubnetMonitorsPresentPublicNetMultiSubnet() {
	s.overrideGetMonitorCount(1, nil)

	origFetch := fetchConfigDb
	s.T().Cleanup(func() { fetchConfigDb = origFetch })
	fetchConfigDb = func(_ context.Context, _ interfaces.StateInterface) (map[string]string, error) {
		return map[string]string{"public_network": "10.0.0.0/24,172.16.0.0/16"}, nil
	}

	net := mocks.NewNetworkIntf(s.T())
	origNetwork := common.Network
	s.T().Cleanup(func() { common.Network = origNetwork })
	common.Network = net

	err := backwardCompatPubnet(context.Background(), nil)
	assert.NoError(s.T(), err)
	net.AssertNotCalled(s.T(), "FindNetworkAddress")
}

// TestCephConfFileRendersMultiSubnetPublicNetwork verifies that a
// comma-delimited public_network value is written verbatim into the rendered
// ceph.conf. It exercises CephConfFile.Render, which renders via the same
// NewCephConfig template that UpdateConfig uses.
func (s *configSuite) TestCephConfFileRendersMultiSubnetPublicNetwork() {
	s.CopyCephConfigs()

	// Redirect GetPathConst to the temp dir that CopyCephConfigs prepared.
	origGetPathConst := constants.GetPathConst
	s.T().Cleanup(func() { constants.GetPathConst = origGetPathConst })
	constants.GetPathConst = func() constants.PathConst {
		return constants.PathConst{
			ConfPath: s.Tmp + "/SNAP_DATA/conf",
		}
	}

	const multiSubnet = "10.0.0.0/24,172.16.0.0/16"

	ccf := CephConfFile{
		FsID:     "aabbccdd-1234-5678-abcd-000000000000",
		RunDir:   "/tmp/testrun",
		Monitors: []string{"1.1.1.1"},
		PubNet:   multiSubnet,
	}
	err := ccf.Render(constants.CephConfFileName)
	assert.NoError(s.T(), err)

	// Read back the rendered file and assert the multi-subnet value is present verbatim.
	rendered, err := os.ReadFile(constants.GetPathConst().ConfPath + "/" + constants.CephConfFileName)
	assert.NoError(s.T(), err)
	assert.Contains(s.T(), string(rendered), "public_network = "+multiSubnet)
}
