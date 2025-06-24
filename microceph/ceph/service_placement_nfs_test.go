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
		Payload: "{\"cluster_id\":\"\"}",
	}

	err := ServicePlacementHandler(context.Background(), s.TestStateInterface, payload)
	assert.ErrorContains(s.T(), err, "expected cluster_id to be non-empty")

	payload.Payload = "{\"cluster_id\":\"foo\",\"v4_min_version\":10}"

	err = ServicePlacementHandler(context.Background(), s.TestStateInterface, payload)
	assert.ErrorContains(s.T(), err, "expected v4_min_version to be in the interval")
}
