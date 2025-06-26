package ceph

import (
	"context"
	"testing"

	"github.com/canonical/microceph/microceph/tests"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/mocks"
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
}

func (s *servicePlacementNFSSuite) TestInvalidPayload() {
	service := "nfs"

	payload := types.EnableService{
		Name:    service,
		Wait:    true,
		Payload: "{\"ClusterID\":\"\"}",
	}

	err := ServicePlacementHandler(context.Background(), s.TestStateInterface, payload)
	assert.ErrorContains(s.T(), err, "expected ClusterID to be non-empty")

	payload.Payload = "{\"ClusterID\":\"foo\",\"V4MinVersion\":10}"

	err = ServicePlacementHandler(context.Background(), s.TestStateInterface, payload)
	assert.ErrorContains(s.T(), err, "expected V4MinVersion to be in the interval")
}

func (s *servicePlacementNFSSuite) TestAddressUnavailable() {
	service := "nfs"

	payload := types.EnableService{
		Name:    service,
		Wait:    true,
		Payload: "{\"ClusterID\":\"foo\",\"ServiceAddress\":\"42.42.42.42:9999\"}",
	}

	err := ServicePlacementHandler(context.Background(), s.TestStateInterface, payload)
	assert.ErrorContains(s.T(), err, "error encountered during address availability check")
}
