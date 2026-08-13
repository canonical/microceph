package ceph

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/mocks"
)

type clusterRemoveSuite struct {
	suite.Suite
}

func TestClusterRemove(t *testing.T) {
	suite.Run(t, new(clusterRemoveSuite))
}

func (s *clusterRemoveSuite) SetupTest() {

}

// TestRemoveNode tests the happy path of node removal
func (s *clusterRemoveSuite) TestRemoveNode() {
	m := mocks.NewClientInterface(s.T())

	client.MClient = m
	m.On("GetClusterMembers", mock.Anything).Return([]string{"foonode", "barnode", "quuxnode"}, nil).Once()
	m.On("GetDisks", mock.Anything).Return(types.Disks{}, nil).Once()

	services := []string{"mon", "mon", "mgr", "mds", "mon", "mgr", "mds", "mon", "mgr", "mds"}
	var servicesData types.Services
	for _, service := range services {
		// Add each service to the array
		servicesData = append(servicesData, types.Service{Service: service})
		// For the first entry, set location to "foonode"
		if service == "mon" && servicesData[0].Location == "" {
			servicesData[0].Location = "foonode"
		}
	}
	m.On("GetServices", mock.Anything).Return(
		servicesData,
		nil,
	)
	m.On("DeleteService", mock.Anything, "foonode", "mon").Return(nil).Once()

	err := removeNode(nil, "foonode", false)

	assert.NoError(s.T(), err)
}

// TestRemoveNodeWithDisks tests that we don't try to delete a node that has OSDs
func (s *clusterRemoveSuite) TestRemoveNodeWithDisks() {
	m := mocks.NewClientInterface(s.T())

	client.MClient = m
	m.On("GetClusterMembers", mock.Anything).Return([]string{"foonode", "barnode", "quuxnode"}, nil).Once()
	m.On("GetDisks", mock.Anything).Return(types.Disks{
		{
			Location: "foonode",
		},
	}, nil).Once()

	err := removeNode(nil, "foonode", false)

	assert.Error(s.T(), err)
}

// TestRemoveNodeLastMon tests that we don't try to delete a node that has the last mon
func (s *clusterRemoveSuite) TestRemoveNodeLastMon() {
	m := mocks.NewClientInterface(s.T())

	client.MClient = m
	m.On("GetClusterMembers", mock.Anything).Return([]string{"foonode", "barnode", "quuxnode"}, nil).Once()
	m.On("GetDisks", mock.Anything).Return(types.Disks{}, nil).Once()
	m.On("GetServices", mock.Anything).Return(
		types.Services{
			{
				Service:  "mon",
				Location: "foonode",
			},
		},
		nil,
	)

	err := removeNode(nil, "foonode", false)

	assert.Error(s.T(), err)
}

// TestRemoveNodeForce tests that we don't check prerequisites and delete a node if forced
func (s *clusterRemoveSuite) TestRemoveNodeForce() {
	m := mocks.NewClientInterface(s.T())

	client.MClient = m

	m.On("GetServices", mock.Anything).Return(
		types.Services{
			{
				Service:  "mon",
				Location: "foonode",
			},
		},
		nil,
	)
	m.On("DeleteService", mock.Anything, "foonode", "mon").Return(nil).Once()

	err := removeNode(nil, "foonode", true)

	assert.NoError(s.T(), err)
}

func (s *clusterRemoveSuite) TestRemoveNodeReturnsServiceDeletionFailure() {
	m := mocks.NewClientInterface(s.T())
	client.MClient = m

	m.On("GetClusterMembers", mock.Anything).Return([]string{"foonode", "barnode", "quuxnode"}, nil).Once()
	m.On("GetDisks", mock.Anything).Return(types.Disks{}, nil).Once()
	services := types.Services{
		{Service: "mon", Location: "foonode"},
		{Service: "mon", Location: "barnode"},
		{Service: "mon", Location: "quuxnode"},
		{Service: "mon", Location: "othernode"},
		{Service: "mgr", Location: "barnode"},
		{Service: "mds", Location: "barnode"},
	}
	m.On("GetServices", mock.Anything).Return(services, nil).Twice()
	monSafetyErr := errors.New("monitor removal would lose quorum")
	m.On("DeleteService", mock.Anything, "foonode", "mon").Return(monSafetyErr).Once()

	err := removeNode(nil, "foonode", false)

	assert.ErrorIs(s.T(), err, monSafetyErr)
	assert.ErrorContains(s.T(), err, `failed to delete service "mon" on node "foonode"`)
}

func (s *clusterRemoveSuite) TestRemoveNodeForceSuppressesServiceDeletionFailure() {
	m := mocks.NewClientInterface(s.T())
	client.MClient = m

	services := types.Services{
		{Service: "mon", Location: "foonode"},
		{Service: "mgr", Location: "foonode"},
	}
	m.On("GetServices", mock.Anything).Return(services, nil).Once()
	monSafetyErr := errors.New("monitor removal would lose quorum")
	mgrDeleteErr := errors.New("manager deletion failed")
	m.On("DeleteService", mock.Anything, "foonode", "mon").Return(monSafetyErr).Once()
	m.On("DeleteService", mock.Anything, "foonode", "mgr").Return(mgrDeleteErr).Once()

	err := removeNode(nil, "foonode", true)

	assert.NoError(s.T(), err)
}

func (s *clusterRemoveSuite) TestDeleteNodeServicesAggregatesFailures() {
	m := mocks.NewClientInterface(s.T())
	client.MClient = m

	services := types.Services{
		{Service: "mon", Location: "foonode"},
		{Service: "mgr", Location: "foonode"},
		{Service: "mds", Location: "barnode"},
	}
	m.On("GetServices", mock.Anything).Return(services, nil).Once()
	monDeleteErr := errors.New("monitor deletion failed")
	mgrDeleteErr := errors.New("manager deletion failed")
	m.On("DeleteService", mock.Anything, "foonode", "mon").Return(monDeleteErr).Once()
	m.On("DeleteService", mock.Anything, "foonode", "mgr").Return(mgrDeleteErr).Once()

	err := deleteNodeServices(nil, "foonode")

	assert.ErrorIs(s.T(), err, monDeleteErr)
	assert.ErrorIs(s.T(), err, mgrDeleteErr)
	assert.ErrorContains(s.T(), err, `failed to delete service "mon" on node "foonode"`)
	assert.ErrorContains(s.T(), err, `failed to delete service "mgr" on node "foonode"`)
}
