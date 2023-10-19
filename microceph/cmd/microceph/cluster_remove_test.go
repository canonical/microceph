package main

import (
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"testing"

	"github.com/stretchr/testify/suite"
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
	m.On("DeleteClusterMember", mock.Anything, "foonode", false).Return(nil).Once()

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

	// assert that we didn't try to delete the node
	m.AssertNotCalled(s.T(), "DeleteClusterMember", mock.Anything, "foonode", false)
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

	// assert that we didn't try to delete the node
	m.AssertNotCalled(s.T(), "DeleteClusterMember", mock.Anything, "foonode", false)
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
	m.On("DeleteClusterMember", mock.Anything, "foonode", true).Return(nil).Once()

	err := removeNode(nil, "foonode", true)

	assert.NoError(s.T(), err)
}
