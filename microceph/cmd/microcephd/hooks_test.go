package main

import (
	"context"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
)

type hooksSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestHooks(t *testing.T) {
	suite.Run(t, new(hooksSuite))
}

func (s *hooksSuite) SetupTest() {
	s.BaseSuite.SetupTest()
	s.CopyCephConfigs()

	s.TestStateInterface = mocks.NewStateInterface(s.T())
	u := api.NewURL()
	u.Host("1.1.1.1")
	state := &mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
	}
	s.TestStateInterface.On("ClusterState").Return(state).Maybe()
}

// ##### Expectations #####

// ##### Unit Tests #####
func (s *hooksSuite) TestPreInit() {
	bootstrapper := mocks.NewBootstrapper(s.T())

	bootstrapper.On("Precheck", mock.Anything, mock.Anything).Return(nil).Once()

	GetBootstrapper = func(bd common.BootstrapConfig) Bootstrapper { return bootstrapper }

	// simple bootstrap input (empty input)
	err := PreInit(context.Background(), s.TestStateInterface.ClusterState(), true, map[string]string{})
	assert.NoError(s.T(), err)
}

func (s *hooksSuite) TestPostBootstrap() {
	bootstrapper := mocks.NewBootstrapper(s.T())

	bootstrapper.On("Precheck", mock.Anything, mock.Anything).Return(nil).Once()
	bootstrapper.On("Bootstrap", mock.Anything, mock.Anything).Return(nil).Once()

	GetBootstrapper = func(bd common.BootstrapConfig) Bootstrapper { return bootstrapper }

	err := PostBootstrap(context.Background(), s.TestStateInterface.ClusterState(), map[string]string{})
	assert.NoError(s.T(), err)
}
