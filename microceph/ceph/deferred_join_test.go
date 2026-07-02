package ceph

import (
	"context"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
)

type deferredJoinSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestDeferredJoin(t *testing.T) {
	suite.Run(t, new(deferredJoinSuite))
}

func (s *deferredJoinSuite) SetupTest() {
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

// withNoopAZ injects a no-op validateAndRecordAZFunc, returning a restore function.
func withNoopAZ() func() {
	orig := validateAndRecordAZFunc
	validateAndRecordAZFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) error {
		return nil
	}
	return func() { validateAndRecordAZFunc = orig }
}

// TestDeferredJoinSkipsAutoPlacement verifies that ceph.Join with DeferCeph=true
// does not create mon/mds/mgr service records and does not start services.
// This is the core UAT-S1.2 assertion: the joining node receives no automatic
// MON/MGR/MDS placement.
func (s *deferredJoinSuite) TestDeferredJoinSkipsAutoPlacement() {
	defer withNoopAZ()()

	// No runner expectations are set: any ceph/snapctl subprocess call would
	// fail the mock assertion. Deferred join must not start any services.
	r := mocks.NewRunner(s.T())
	common.ProcessExec = r

	jc := common.JoinConfig{DeferCeph: true}
	err := Join(context.Background(), s.TestStateInterface, jc)
	assert.NoError(s.T(), err)
}

// TestDeferredJoinRecordsAZ verifies that deferred join still calls the AZ
// recording function so availability zone metadata is persisted for later use.
func (s *deferredJoinSuite) TestDeferredJoinRecordsAZ() {
	azRecorded := false
	orig := validateAndRecordAZFunc
	validateAndRecordAZFunc = func(_ context.Context, _ interfaces.StateInterface, az string) error {
		if az == "az-1" {
			azRecorded = true
		}
		return nil
	}
	defer func() { validateAndRecordAZFunc = orig }()

	jc := common.JoinConfig{DeferCeph: true, AvailabilityZone: "az-1"}
	err := Join(context.Background(), s.TestStateInterface, jc)
	assert.NoError(s.T(), err)
	assert.True(s.T(), azRecorded, "expected AZ to be recorded during deferred join")
}

// TestNonDeferredJoinRunsLegacyPath verifies that without DeferCeph the legacy
// join path is used (regression / UAT-S1.6). The legacy path calls
// updateConfigFunc, which we inject to return nil. The key assertion is that
// updateConfigFunc is called (proving the legacy path was entered).
func (s *deferredJoinSuite) TestNonDeferredJoinRunsLegacyPath() {
	defer withNoopAZ()()

	updateConfigCalled := false
	orig := updateConfigFunc
	updateConfigFunc = func(_ context.Context, _ interfaces.StateInterface) error {
		updateConfigCalled = true
		return nil
	}
	defer func() { updateConfigFunc = orig }()

	store := newMockJoinHostTagStore()
	defer withMockJoinStore(store)()

	// Inject a no-op for the legacy service placement so the test doesn't need
	// a real database. The key assertion is that updateConfigFunc was called.
	origSvc := legacyJoinServicesFunc
	legacyJoinServicesFunc = func(_ context.Context, _ interfaces.StateInterface) error {
		return nil
	}
	defer func() { legacyJoinServicesFunc = origSvc }()

	// The legacy path will proceed past the deferred check, call UpdateConfig,
	// then call legacyJoinServicesFunc. The key assertion is that updateConfigFunc
	// was called (proving the legacy path was entered, not the deferred short-circuit).
	jc := common.JoinConfig{DeferCeph: false}
	_ = Join(context.Background(), s.TestStateInterface, jc)
	assert.True(s.T(), updateConfigCalled, "expected UpdateConfig to be called in legacy (non-deferred) join")
}
