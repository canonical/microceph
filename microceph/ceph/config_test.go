package ceph

import (
	"encoding/json"
	"testing"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type configSuite struct {
	baseSuite
}

func TestConfig(t *testing.T) {
	suite.Run(t, new(configSuite))
}

// Set up test suite
func (s *configSuite) SetupTest() {
	s.baseSuite.SetupTest()
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
	processExec = r

	err := SetConfigItem(t)
	assert.NoError(s.T(), err)
}

func (s *configSuite) TestGetConfig() {
	t := types.Config{Key: "cluster_network", Value: "0.0.0.0/16"}

	r := mocks.NewRunner(s.T())
	addConfigOpExpectations(r, "get", "mon", t.Key, t.Value)
	processExec = r

	_, err := GetConfigItem(t)
	assert.NoError(s.T(), err)
}

func (s *configSuite) TestResetConfig() {
	t := types.Config{Key: "cluster_network", Value: "0.0.0.0/16"}

	r := mocks.NewRunner(s.T())
	addConfigOpExpectations(r, "rm", "global", t.Key, t.Value)
	processExec = r

	err := RemoveConfigItem(t)
	assert.NoError(s.T(), err)
}

func (s *configSuite) TestListConfig() {
	t := types.Config{Key: "cluster_network", Value: "0.0.0.0/16"}

	r := mocks.NewRunner(s.T())
	addListConfigExpectations(r, t.Key, t.Value)
	processExec = r

	configs, err := ListConfigs()
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), configs[0].Key, t.Key)
	assert.Equal(s.T(), configs[0].Value, t.Value)
}
