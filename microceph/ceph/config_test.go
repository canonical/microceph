package ceph

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/canonical/microceph/microceph/common"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
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
