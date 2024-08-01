package ceph

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/tests"
	"github.com/canonical/microcluster/v2/state"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type ClientConfigSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestClientConfig(t *testing.T) {
	suite.Run(t, new(ClientConfigSuite))
}

func (ccs *ClientConfigSuite) SetupTest() {
	ccs.BaseSuite.SetupTest()

	ccs.TestStateInterface = mocks.NewStateInterface(ccs.T())
	u := api.NewURL()
	state := &mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
	}

	ccs.TestStateInterface.On("ClusterState").Return(state)
}

func addGetHostConfigsExpectation(mci *mocks.ClientConfigQueryIntf, cs state.State, hostname string) {
	output := database.ClientConfigItems{}
	count := 0
	for configKey, field := range GetClientConfigSet() {
		count++
		output = append(output, database.ClientConfigItem{
			ID:    count,
			Host:  hostname,
			Key:   configKey,
			Value: fmt.Sprintf("%v", field),
		})
	}

	mci.On("GetAllForHost", cs, hostname).Return(output, nil)
}

func (ccs *ClientConfigSuite) TestFetchHostConfig() {
	hostname := "testHostname"

	// Mock Client config query interface.
	ccq := mocks.NewClientConfigQueryIntf(ccs.T())
	addGetHostConfigsExpectation(ccq, ccs.TestStateInterface.ClusterState(), hostname)
	database.ClientConfigQuery = ccq

	configs, err := GetClientConfigForHost(context.Background(), ccs.TestStateInterface, hostname)
	assert.NoError(ccs.T(), err)

	// check fields
	metaConfigs := reflect.ValueOf(configs)
	for i := 0; i < metaConfigs.NumField(); i++ {
		assert.Equal(ccs.T(), metaConfigs.Field(i).Interface(), metaConfigs.Type().Field(i).Name)
	}
}
