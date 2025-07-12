package ceph

import (
	"context"
	"testing"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type servicePlacementNFSSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestServicesPlacementNFS(t *testing.T) {
	suite.Run(t, new(servicePlacementNFSSuite))
}

// Set up test suite
func (s *servicePlacementNFSSuite) SetupTest() {
	s.BaseSuite.SetupTest()
	s.TestStateInterface = mocks.NewStateInterface(s.T())
}

func (s *servicePlacementNFSSuite) TestInvalidPayload() {
	service := "nfs"

	payload := types.EnableService{
		Name:    service,
		Wait:    true,
		Payload: "{\"cluster_id\":\"\"}",
	}

	err := ServicePlacementHandler(context.Background(), s.TestStateInterface, payload)
	assert.ErrorContains(s.T(), err, "expected cluster_id to be non-empty")

	payload.Payload = "{\"cluster_id\":\"foo\",\"v4_min_version\":10}"

	err = ServicePlacementHandler(context.Background(), s.TestStateInterface, payload)
	assert.ErrorContains(s.T(), err, "expected v4_min_version '10' to be in the interval")

	payload.Payload = "{\"cluster_id\":\"foo\",\"bind_address\":\"10.20.30\"}"

	err = ServicePlacementHandler(context.Background(), s.TestStateInterface, payload)
	assert.ErrorContains(s.T(), err, "bind_address '10.20.30' could not be parsed")

	payload.Payload = "{\"cluster_id\":\"foo\",\"bind_port\":99999}"

	err = ServicePlacementHandler(context.Background(), s.TestStateInterface, payload)
	assert.ErrorContains(s.T(), err, "expected bind_port number '99999' to be in range [1-49151]")
}

func (s *servicePlacementNFSSuite) TestAddressUnavailable() {
	service := "nfs"

	payload := types.EnableService{
		Name:    service,
		Wait:    true,
		Payload: "{\"cluster_id\":\"foo\",\"bind_address\":\"42.42.42.42\"}",
	}

	err := ServicePlacementHandler(context.Background(), s.TestStateInterface, payload)
	assert.ErrorContains(s.T(), err, "error encountered during address availability check")
}

func (s *servicePlacementNFSSuite) TestDBUpdate() {
	nfs := NFSServicePlacement{
		ClusterID:    "foo",
		V4MinVersion: 2,
		BindAddress:  "42.42.42.42",
		BindPort:     9999,
	}

	groupConfig := database.NFSServiceGroupConfig{
                V4MinVersion: nfs.V4MinVersion,
	}

        serviceInfo := database.NFSServiceInfo{
                BindAddress: nfs.BindAddress,
                BindPort:    nfs.BindPort,
        }

	db := mocks.NewGroupedServiceQueryIntf(s.T())

	// AddNew call
	ctx := context.Background()
	db.On("AddNew", []interface{}{ctx, s.TestStateInterface, "nfs", nfs.ClusterID, groupConfig, serviceInfo}...).Return(nil).Once()

	// patch GroupedServicesQuery
	originalDB := database.GroupedServicesQuery
	defer func() { database.GroupedServicesQuery = originalDB }()
	database.GroupedServicesQuery = db

	err := nfs.DbUpdate(ctx, s.TestStateInterface)

	assert.NoError(s.T(), err)
}
