package ceph

import (
	"context"
	"fmt"
	"github.com/canonical/microceph/microceph/common"
	"testing"

	"github.com/canonical/microceph/microceph/tests"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type servicesPlacementSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestServicesPlacement(t *testing.T) {
	suite.Run(t, new(servicesPlacementSuite))
}

// Set up test suite
func (s *servicesPlacementSuite) SetupTest() {
	s.BaseSuite.SetupTest()
}

func addSnapServiceActiveExpectations(r *mocks.Runner, service string, retStr string, retErr error) {
	r.On("RunCommand", []interface{}{
		"snapctl", "services", fmt.Sprintf("microceph.%s", service),
	}...).Return(retStr, retErr).Once()
}

func addPlacementServiceInitFailExpectation(sp *mocks.PlacementIntf, s *mocks.StateInterface, payload types.EnableService) {
	sp.On("PopulateParams", s, payload.Payload).Return(nil).Once()
	sp.On("HospitalityCheck", s).Return(nil).Once()
	sp.On("ServiceInit", s).Return(fmt.Errorf("ERROR")).Once()
}

func addPostPlacementCheckFailExpectation(sp *mocks.PlacementIntf, s *mocks.StateInterface, payload types.EnableService) {
	sp.On("PopulateParams", s, payload.Payload).Return(nil).Once()
	sp.On("HospitalityCheck", s).Return(nil).Once()
	sp.On("ServiceInit", s).Return(nil).Once()
	sp.On("PostPlacementCheck", s).Return(fmt.Errorf("ERROR")).Once()
}

func addDbUpdateFailExpectation(sp *mocks.PlacementIntf, s *mocks.StateInterface, payload types.EnableService) {
	sp.On("PopulateParams", s, payload.Payload).Return(nil).Once()
	sp.On("HospitalityCheck", s).Return(nil).Once()
	sp.On("ServiceInit", s).Return(nil).Once()
	sp.On("PostPlacementCheck", s).Return(nil).Once()
	sp.On("DbUpdate", s).Return(fmt.Errorf("ERROR")).Once()
}

func (s *servicesPlacementSuite) TestUnknownServiceFailure() {
	payload := types.EnableService{
		Name:    "unknowService",
		Wait:    true,
		Payload: "",
	}

	// Check Enable Service fails for unregistered services.
	err := ServicePlacementHandler(context.Background(), s.TestStateInterface, payload)
	assert.Error(s.T(), err)
}

func (s *servicesPlacementSuite) TestIllStructuredPayloadFailure() {
	service := "rgw"

	payload := types.EnableService{
		Name:    service,
		Wait:    true,
		Payload: "\"Port\":80", // Json String does not have {}
	}

	// Check Enable Service fails for unregistered services.
	err := ServicePlacementHandler(context.Background(), s.TestStateInterface, payload)
	assert.ErrorContains(s.T(), err, "failed to populate the payload")
}

func (s *servicesPlacementSuite) TestHospitalityCheckFailure() {
	service := "rgw"

	r := mocks.NewRunner(s.T())
	common.ProcessExec = r
	addSnapServiceActiveExpectations(r, service, "active", nil)

	payload := types.EnableService{
		Name:    service,
		Wait:    true,
		Payload: "{\"Port\":80}",
	}

	// Check Enable Service fails for unregistered services.
	err := ServicePlacementHandler(context.Background(), s.TestStateInterface, payload)
	assert.ErrorContains(s.T(), err, "host failed hospitality check")
}

func (s *servicesPlacementSuite) TestServiceInitFailure() {
	service := "mon"
	payload := types.EnableService{
		Name: service,
		Wait: true,
	}

	sp := mocks.NewPlacementIntf(s.T())
	addPlacementServiceInitFailExpectation(sp, s.TestStateInterface, payload)

	// Check Enable Service fails for unregistered services.
	err := EnableService(context.Background(), s.TestStateInterface, payload, sp)
	assert.ErrorContains(s.T(), err, "failed to initialise")
}

func (s *servicesPlacementSuite) TestPostPlacementCheckFailure() {
	service := "mon"
	payload := types.EnableService{
		Name: service,
		Wait: true,
	}

	sp := mocks.NewPlacementIntf(s.T())
	addPostPlacementCheckFailExpectation(sp, s.TestStateInterface, payload)

	// Check Enable Service fails for unregistered services.
	err := EnableService(context.Background(), s.TestStateInterface, payload, sp)
	assert.ErrorContains(s.T(), err, "service unable to sustain on host")
}

func (s *servicesPlacementSuite) TestDbUpdateFailure() {
	service := "mon"
	payload := types.EnableService{
		Name: service,
		Wait: true,
	}

	sp := mocks.NewPlacementIntf(s.T())
	addDbUpdateFailExpectation(sp, s.TestStateInterface, payload)

	// Check Enable Service fails for unregistered services.
	err := EnableService(context.Background(), s.TestStateInterface, payload, sp)
	assert.ErrorContains(s.T(), err, "failed to add DB record for")
}
