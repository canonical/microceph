package main

import (
	"context"
	"testing"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
)

type deferredBootstrapSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestDeferredBootstrap(t *testing.T) {
	suite.Run(t, new(deferredBootstrapSuite))
}

func (s *deferredBootstrapSuite) SetupTest() {
	s.BaseSuite.SetupTest()
	s.CopyCephConfigs()

	s.TestStateInterface = mocks.NewStateInterface(s.T())
	u := api.NewURL()
	u.Host("1.1.1.1")
	state := &mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
		Cert:        &shared.CertInfo{},
	}
	s.TestStateInterface.On("ClusterState").Return(state).Maybe()
}

// withNoopLifecycle injects a no-op setLifecycleStateFunc.
func withNoopLifecycle() func() {
	orig := setLifecycleStateFunc
	setLifecycleStateFunc = func(_ context.Context, _ interfaces.StateInterface, _ database.ClusterLifecycle) error {
		return nil
	}
	return func() { setLifecycleStateFunc = orig }
}

// TestDeferredBootstrapperPrefillIsNoop verifies Prefill records the AZ.
func (s *deferredBootstrapSuite) TestDeferredBootstrapperPrefillIsNoop() {
	db := &DeferredBootstrapper{}
	err := db.Prefill(common.BootstrapConfig{AvailabilityZone: "az-0"}, s.TestStateInterface)
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "az-0", db.AvailabilityZone)
}

// TestDeferredBootstrapperPrecheckValidatesAZ verifies Precheck rejects an
// invalid AZ before MicroCluster state is modified.
func (s *deferredBootstrapSuite) TestDeferredBootstrapperPrecheckValidatesAZ() {
	db := &DeferredBootstrapper{AvailabilityZone: "az-0"}
	err := db.Precheck(context.Background(), s.TestStateInterface)
	assert.NoError(s.T(), err)

	db = &DeferredBootstrapper{AvailabilityZone: "invalid zone!"}
	err = db.Precheck(context.Background(), s.TestStateInterface)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "invalid availability zone name")
}

// TestDeferredBootstrapperBootstrapRecordsNotBootstrapped verifies that
// Bootstrap records the lifecycle state as not_bootstrapped via the injectable
// setLifecycleStateFunc, and does not create any Ceph artifacts.
func (s *deferredBootstrapSuite) TestDeferredBootstrapperBootstrapRecordsNotBootstrapped() {
	defer withNoopLifecycle()()

	var captured database.ClusterLifecycle
	orig := setLifecycleStateFunc
	setLifecycleStateFunc = func(_ context.Context, _ interfaces.StateInterface, lc database.ClusterLifecycle) error {
		captured = lc
		return nil
	}
	defer func() { setLifecycleStateFunc = orig }()

	db := &DeferredBootstrapper{}
	err := db.Bootstrap(context.Background(), s.TestStateInterface)
	assert.NoError(s.T(), err)

	assert.Equal(s.T(), database.CephStateNotBootstrapped, captured.CephBootstrapState)
}

// TestDeferredBootstrapperBootstrapRecordsAZ (N4) verifies that Bootstrap
// records the availability zone as a host_tag so subsequent deferred joins with
// --availability-zone are not rejected by validateJoinAZ.
func (s *deferredBootstrapSuite) TestDeferredBootstrapperBootstrapRecordsAZ() {
	defer withNoopLifecycle()()

	azRecorded := false
	var azMember, azValue string
	origAZ := recordDeferredAZFunc
	recordDeferredAZFunc = func(_ context.Context, _ interfaces.StateInterface, member string, az string) error {
		azRecorded = true
		azMember = member
		azValue = az
		return nil
	}
	defer func() { recordDeferredAZFunc = origAZ }()

	db := &DeferredBootstrapper{AvailabilityZone: "az-0"}
	err := db.Bootstrap(context.Background(), s.TestStateInterface)
	assert.NoError(s.T(), err)
	assert.True(s.T(), azRecorded, "expected AZ to be recorded during deferred bootstrap")
	assert.Equal(s.T(), "foohost", azMember)
	assert.Equal(s.T(), "az-0", azValue)
}

// TestDeferredBootstrapperBootstrapNoAZDoesNotRecord verifies that when no AZ
// is provided, Bootstrap does not attempt to record an AZ tag.
func (s *deferredBootstrapSuite) TestDeferredBootstrapperBootstrapNoAZDoesNotRecord() {
	defer withNoopLifecycle()()

	azCalled := false
	origAZ := recordDeferredAZFunc
	recordDeferredAZFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ string) error {
		azCalled = true
		return nil
	}
	defer func() { recordDeferredAZFunc = origAZ }()

	db := &DeferredBootstrapper{}
	err := db.Bootstrap(context.Background(), s.TestStateInterface)
	assert.NoError(s.T(), err)
	assert.False(s.T(), azCalled, "AZ recording must not be called when no AZ is set")
}

// TestDeferredBootstrapperBootstrapInvalidAZ verifies that an invalid AZ name
// is rejected during deferred bootstrap.
func (s *deferredBootstrapSuite) TestDeferredBootstrapperBootstrapInvalidAZ() {
	lifecycleCalled := false
	orig := setLifecycleStateFunc
	setLifecycleStateFunc = func(_ context.Context, _ interfaces.StateInterface, _ database.ClusterLifecycle) error {
		lifecycleCalled = true
		return nil
	}
	defer func() { setLifecycleStateFunc = orig }()

	db := &DeferredBootstrapper{AvailabilityZone: "invalid zone!"}
	err := db.Bootstrap(context.Background(), s.TestStateInterface)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "invalid availability zone name")
	assert.False(s.T(), lifecycleCalled, "invalid AZ must be rejected before lifecycle writes")
}

// TestGetBootstrapperReturnsDeferred verifies that getBootstrapper selects the
// DeferredBootstrapper when DeferCeph is set in the bootstrap config.
func (s *deferredBootstrapSuite) TestGetBootstrapperReturnsDeferred() {
	bd := common.BootstrapConfig{DeferCeph: true}
	bs, err := getBootstrapper(bd, s.TestStateInterface)
	assert.NoError(s.T(), err)
	_, ok := bs.(*DeferredBootstrapper)
	assert.True(s.T(), ok, "expected DeferredBootstrapper when DeferCeph is true")
}

// TestGetBootstrapperReturnsSimpleWhenNotDeferred verifies legacy behaviour is
// preserved: without DeferCeph, SimpleBootstrapper is selected (UAT-S1.6).
func (s *deferredBootstrapSuite) TestGetBootstrapperReturnsSimpleWhenNotDeferred() {
	bd := common.BootstrapConfig{MonIp: "1.1.1.1", PublicNet: "1.1.1.1/24", ClusterNet: "1.1.1.1/24"}
	bs, err := getBootstrapper(bd, s.TestStateInterface)
	assert.NoError(s.T(), err)
	_, ok := bs.(*SimpleBootstrapper)
	assert.True(s.T(), ok, "expected SimpleBootstrapper when DeferCeph is false")
}

// TestDeferredBootstrapSkipsCephArtifacts verifies that deferred bootstrap does
// not invoke any ceph command subprocesses (no FSID/keyring/ceph.conf creation).
// This is the UAT-S1.1 core assertion: no Ceph artifacts are produced.
func (s *deferredBootstrapSuite) TestDeferredBootstrapSkipsCephArtifacts() {
	defer withNoopLifecycle()()

	r := mocks.NewRunner(s.T())
	common.ProcessExec = r
	// No ceph subprocess expectations are set: any ceph-authtool/monmaptool/ceph
	// call would fail the mock assertion.

	db := &DeferredBootstrapper{}
	err := db.Bootstrap(context.Background(), s.TestStateInterface)
	assert.NoError(s.T(), err)
}

// TestDeferredBootstrapPropagatesLifecycleError verifies that a failure to
// persist lifecycle state is returned as an error.
func (s *deferredBootstrapSuite) TestDeferredBootstrapPropagatesLifecycleError() {
	orig := setLifecycleStateFunc
	setLifecycleStateFunc = func(_ context.Context, _ interfaces.StateInterface, _ database.ClusterLifecycle) error {
		return assert.AnError
	}
	defer func() { setLifecycleStateFunc = orig }()

	db := &DeferredBootstrapper{}
	err := db.Bootstrap(context.Background(), s.TestStateInterface)
	assert.Error(s.T(), err)
}

// Ensure the mock is exercised (suppress unused import warning for mock).
var _ = mock.Anything
