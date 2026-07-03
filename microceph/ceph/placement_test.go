package ceph

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
)

type placementSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestPlacement(t *testing.T) {
	suite.Run(t, new(placementSuite))
}

func (s *placementSuite) SetupTest() {
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

	// Default: three known members.
	origMembers := GetClusterMemberNamesFunc
	GetClusterMemberNamesFunc = func(_ context.Context, _ interfaces.StateInterface) ([]string, error) {
		return []string{"node-a", "node-b", "node-c"}, nil
	}
	s.T().Cleanup(func() { GetClusterMemberNamesFunc = origMembers })

	// Default: Ceph is bootstrapped so the pre-bootstrap guard does not block.
	origBootstrapped := cephIsBootstrappedFunc
	cephIsBootstrappedFunc = func(_ context.Context, _ interfaces.StateInterface) (bool, error) {
		return true, nil
	}
	s.T().Cleanup(func() { cephIsBootstrappedFunc = origBootstrapped })

	// Default: all control services are ready so the keep-one readiness guard
	// does not block. Tests that exercise the readiness guard override this.
	origReady := controlServiceReadyFunc
	controlServiceReadyFunc = func(_ context.Context, _ string, _ string) (bool, error) {
		return true, nil
	}
	s.T().Cleanup(func() { controlServiceReadyFunc = origReady })

	// Default: no polling — readiness check runs once. Tests that exercise
	// polling override this.
	origTimeout := controlReadinessTimeout
	controlReadinessTimeout = 0
	s.T().Cleanup(func() { controlReadinessTimeout = origTimeout })
}

// boolPtr returns a pointer to the given bool.
func boolPtr(b bool) *bool { return &b }

// withObservedControl injects a fixed observed control services map.
func withObservedControl(observed map[string]map[string]bool) func() {
	orig := getObservedControlServicesFunc
	getObservedControlServicesFunc = func(_ context.Context, _ interfaces.StateInterface) (map[string]map[string]bool, error) {
		// Deep copy so the test's map isn't mutated.
		result := make(map[string]map[string]bool)
		for svc, members := range observed {
			result[svc] = make(map[string]bool)
			for m := range members {
				result[svc][m] = true
			}
		}
		return result, nil
	}
	return func() { getObservedControlServicesFunc = orig }
}

// addRemoveRecorder tracks add/remove calls in a single ordered event log
// so tests can assert add-before-remove ordering.
type addRemoveRecorder struct {
	events []string // "add:svc:member" or "remove:svc:member"
}

func withAddRemoveRecorder() (*addRemoveRecorder, func()) {
	rec := &addRemoveRecorder{}
	origAdd := addControlServiceFunc
	origRemove := removeControlServiceFunc
	addControlServiceFunc = func(_ context.Context, _ interfaces.StateInterface, member string, service string) error {
		rec.events = append(rec.events, fmt.Sprintf("add:%s:%s", service, member))
		return nil
	}
	removeControlServiceFunc = func(_ context.Context, _ interfaces.StateInterface, member string, service string) error {
		rec.events = append(rec.events, fmt.Sprintf("remove:%s:%s", service, member))
		return nil
	}
	return rec, func() {
		addControlServiceFunc = origAdd
		removeControlServiceFunc = origRemove
	}
}

// adds returns only the add events.
func (r *addRemoveRecorder) adds() []string {
	var result []string
	for _, e := range r.events {
		if len(e) > 4 && e[:4] == "add:" {
			result = append(result, e[4:])
		}
	}
	return result
}

// removes returns only the remove events.
func (r *addRemoveRecorder) removes() []string {
	var result []string
	for _, e := range r.events {
		if len(e) > 7 && e[:7] == "remove:" {
			result = append(result, e[7:])
		}
	}
	return result
}

// allAddsBeforeAllRemoves returns true if every add event precedes every
// remove event in the ordered event log.
func (r *addRemoveRecorder) allAddsBeforeAllRemoves() bool {
	firstRemoveIdx := -1
	lastAddIdx := -1
	for i, e := range r.events {
		if len(e) > 4 && e[:4] == "add:" {
			lastAddIdx = i
		}
		if len(e) > 7 && e[:7] == "remove:" && firstRemoveIdx == -1 {
			firstRemoveIdx = i
		}
	}
	if firstRemoveIdx == -1 || lastAddIdx == -1 {
		return true // no adds or no removes, trivially ordered
	}
	return lastAddIdx < firstRemoveIdx
}

// TestPlacementEmptyMapNoOps verifies that an empty members map performs no
// service operations (UAT-S1.5 precondition).
func (s *placementSuite) TestPlacementEmptyMapNoOps() {
	rec, restore := withAddRemoveRecorder()
	defer restore()

	policy := types.PlacementPolicy{Mode: "reconcile", Members: map[string]types.MemberPlacement{}}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	assert.NoError(s.T(), err)
	assert.Empty(s.T(), rec.adds())
	assert.Empty(s.T(), rec.removes())
}

// TestPlacementAddControl verifies that control:true adds MON/MGR/MDS on the member.
func (s *placementSuite) TestPlacementAddControl() {
	defer withObservedControl(map[string]map[string]bool{
		"mon": {}, "mgr": {}, "mds": {},
	})()
	rec, restore := withAddRemoveRecorder()
	defer restore()

	policy := types.PlacementPolicy{
		Mode: "reconcile",
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: boolPtr(true)},
		},
	}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	assert.NoError(s.T(), err)
	assert.ElementsMatch(s.T(), []string{"mon:node-a", "mgr:node-a", "mds:node-a"}, rec.adds())
	assert.Empty(s.T(), rec.removes())
}

// TestPlacementRemoveControl verifies that control:false removes MON/MGR/MDS
// from the member (UAT-S1.5).
func (s *placementSuite) TestPlacementRemoveControl() {
	defer withObservedControl(map[string]map[string]bool{
		"mon": {"node-a": true, "node-b": true},
		"mgr": {"node-a": true, "node-b": true},
		"mds": {"node-a": true, "node-b": true},
	})()
	rec, restore := withAddRemoveRecorder()
	defer restore()

	policy := types.PlacementPolicy{
		Mode: "reconcile",
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: boolPtr(false)},
			"node-b": {Control: boolPtr(true)},
		},
	}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	assert.NoError(s.T(), err)
	assert.ElementsMatch(s.T(), []string{"mon:node-a", "mgr:node-a", "mds:node-a"}, rec.removes())
}

// TestPlacementKeepOneInvariant verifies that the engine refuses to remove the
// last viable MON, MGR, or MDS and surfaces the refusal as an error (UAT-S1.5 / N6).
func (s *placementSuite) TestPlacementKeepOneInvariant() {
	defer withObservedControl(map[string]map[string]bool{
		"mon": {"node-a": true},
		"mgr": {"node-a": true},
		"mds": {"node-a": true},
	})()
	rec, restore := withAddRemoveRecorder()
	defer restore()

	policy := types.PlacementPolicy{
		Mode: "reconcile",
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: boolPtr(false)},
		},
	}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	assert.Error(s.T(), err, "keep-one refusal must be surfaced as an error")
	assert.ErrorIs(s.T(), err, ErrKeepOneInvariant)
	assert.Empty(s.T(), rec.removes(), "must not remove last control service")
}

// TestPlacementMigrateControl verifies add-before-remove: new services are
// added before old ones are removed (UAT-S1.5).
func (s *placementSuite) TestPlacementMigrateControl() {
	defer withObservedControl(map[string]map[string]bool{
		"mon": {"node-a": true},
		"mgr": {"node-a": true},
		"mds": {"node-a": true},
	})()
	rec, restore := withAddRemoveRecorder()
	defer restore()

	policy := types.PlacementPolicy{
		Mode: "reconcile",
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: boolPtr(false)},
			"node-b": {Control: boolPtr(true)},
		},
	}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	assert.NoError(s.T(), err)

	// All adds must precede all removes in the ordered event log.
	assert.True(s.T(), rec.allAddsBeforeAllRemoves(), "adds must precede removes: %v", rec.events)

	// Verify node-b services were added and node-a services removed.
	addSet := make(map[string]bool)
	for _, a := range rec.adds() {
		addSet[a] = true
	}
	assert.True(s.T(), addSet["mon:node-b"])
	assert.True(s.T(), addSet["mgr:node-b"])
	assert.True(s.T(), addSet["mds:node-b"])

	removeSet := make(map[string]bool)
	for _, r := range rec.removes() {
		removeSet[r] = true
	}
	assert.True(s.T(), removeSet["mon:node-a"])
	assert.True(s.T(), removeSet["mgr:node-a"])
	assert.True(s.T(), removeSet["mds:node-a"])
}

// TestPlacementOmittedFieldsUntouched verifies that omitted service fields and
// omitted members are not touched (UAT-S1.5 / precedence rule 8).
func (s *placementSuite) TestPlacementOmittedFieldsUntouched() {
	defer withObservedControl(map[string]map[string]bool{
		"mon": {"node-c": true},
		"mgr": {"node-c": true},
		"mds": {"node-c": true},
	})()
	rec, restore := withAddRemoveRecorder()
	defer restore()

	// node-a has control:true; node-c is omitted from the map entirely.
	policy := types.PlacementPolicy{
		Mode: "reconcile",
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: boolPtr(true)},
		},
	}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	assert.NoError(s.T(), err)

	// node-c should not be touched (no adds or removes for node-c).
	for _, r := range rec.removes() {
		assert.NotContains(s.T(), r, "node-c")
	}
	for _, a := range rec.adds() {
		assert.NotContains(s.T(), a, "node-c")
	}
}

// TestPlacementUnknownMemberRejected verifies that unknown members in the map
// are rejected (UAT-S1.5).
func (s *placementSuite) TestPlacementUnknownMemberRejected() {
	rec, restore := withAddRemoveRecorder()
	defer restore()

	policy := types.PlacementPolicy{
		Mode: "reconcile",
		Members: map[string]types.MemberPlacement{
			"unknown-node": {Control: boolPtr(true)},
		},
	}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	assert.Error(s.T(), err)
	assert.ErrorIs(s.T(), err, ErrUnknownPlacementMember)
	assert.Empty(s.T(), rec.adds())
}

// TestPlacementIdempotent verifies that applying the same policy twice with no
// observed changes is a no-op.
func (s *placementSuite) TestPlacementIdempotent() {
	defer withObservedControl(map[string]map[string]bool{
		"mon": {"node-a": true},
		"mgr": {"node-a": true},
		"mds": {"node-a": true},
	})()
	rec, restore := withAddRemoveRecorder()
	defer restore()

	policy := types.PlacementPolicy{
		Mode: "reconcile",
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: boolPtr(true)},
		},
	}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	assert.NoError(s.T(), err)
	assert.Empty(s.T(), rec.adds())
	assert.Empty(s.T(), rec.removes())
}

// TestPlacementControlFalseOnMemberWithNoService verifies that control:false on
// a member that doesn't have the service is a no-op (no removal needed).
func (s *placementSuite) TestPlacementControlFalseOnMemberWithNoService() {
	defer withObservedControl(map[string]map[string]bool{
		"mon": {"node-b": true},
		"mgr": {"node-b": true},
		"mds": {"node-b": true},
	})()
	rec, restore := withAddRemoveRecorder()
	defer restore()

	policy := types.PlacementPolicy{
		Mode: "reconcile",
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: boolPtr(false)}, // node-a has no services
			"node-b": {Control: boolPtr(true)},
		},
	}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	assert.NoError(s.T(), err)
	assert.Empty(s.T(), rec.removes(), "no removals for a member without services")
}

// TestPlacementMultiRemovalConvergence (B1) verifies that when two control:false
// members have services and one control:true keeper is added, BOTH false
// members are removed and the keeper retains mon/mgr/mds. This was a bug where
// retainCount-- caused non-convergence.
func (s *placementSuite) TestPlacementMultiRemovalConvergence() {
	defer withObservedControl(map[string]map[string]bool{
		"mon": {"node-a": true, "node-b": true},
		"mgr": {"node-a": true, "node-b": true},
		"mds": {"node-a": true, "node-b": true},
	})()
	rec, restore := withAddRemoveRecorder()
	defer restore()

	policy := types.PlacementPolicy{
		Mode: "reconcile",
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: boolPtr(false)},
			"node-b": {Control: boolPtr(false)},
			"node-c": {Control: boolPtr(true)},
		},
	}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	assert.NoError(s.T(), err)

	// node-c should get mon/mgr/mds added.
	addSet := make(map[string]bool)
	for _, a := range rec.adds() {
		addSet[a] = true
	}
	assert.True(s.T(), addSet["mon:node-c"])
	assert.True(s.T(), addSet["mgr:node-c"])
	assert.True(s.T(), addSet["mds:node-c"])

	// Both node-a and node-b should have mon/mgr/mds removed.
	removeSet := make(map[string]bool)
	for _, r := range rec.removes() {
		removeSet[r] = true
	}
	for _, svc := range controlServices {
		assert.True(s.T(), removeSet[fmt.Sprintf("%s:node-a", svc)], "expected %s removed from node-a", svc)
		assert.True(s.T(), removeSet[fmt.Sprintf("%s:node-b", svc)], "expected %s removed from node-b", svc)
	}
}

// TestPlacementOmittedControlOnPresentMember (T1) verifies that a present
// member with Control=nil (omitted field) is not touched for control service
// placement, even if it currently has mon/mgr/mds.
func (s *placementSuite) TestPlacementOmittedControlOnPresentMember() {
	defer withObservedControl(map[string]map[string]bool{
		"mon": {"node-a": true},
		"mgr": {"node-a": true},
		"mds": {"node-a": true},
	})()
	rec, restore := withAddRemoveRecorder()
	defer restore()

	// node-a is present but Control is nil (omitted). node-b gets control:true.
	policy := types.PlacementPolicy{
		Mode: "reconcile",
		Members: map[string]types.MemberPlacement{
			"node-a": {}, // Control omitted = untouched
			"node-b": {Control: boolPtr(true)},
		},
	}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	assert.NoError(s.T(), err)

	// node-a must not be touched (no adds or removes for node-a).
	for _, r := range rec.removes() {
		assert.NotContains(s.T(), r, "node-a")
	}
	for _, a := range rec.adds() {
		assert.NotContains(s.T(), a, "node-a")
	}
}

// TestPlacementPreBootstrapRejectsNonEmptyPolicy verifies that a non-empty
// placement policy is rejected with a clear message when Ceph is not
// bootstrapped, and no add/remove operations are attempted (FIX 3).
func (s *placementSuite) TestPlacementPreBootstrapRejectsNonEmptyPolicy() {
	origBootstrapped := cephIsBootstrappedFunc
	cephIsBootstrappedFunc = func(_ context.Context, _ interfaces.StateInterface) (bool, error) {
		return false, nil
	}
	defer func() { cephIsBootstrappedFunc = origBootstrapped }()

	rec, restore := withAddRemoveRecorder()
	defer restore()

	policy := types.PlacementPolicy{
		Mode: "reconcile",
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: boolPtr(true)},
		},
	}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	assert.Error(s.T(), err)
	assert.ErrorIs(s.T(), err, ErrCephNotBootstrapped)
	assert.Empty(s.T(), rec.adds(), "no service operations must run pre-bootstrap")
	assert.Empty(s.T(), rec.removes(), "no service operations must run pre-bootstrap")
}

// TestPlacementPreBootstrapAllowsEmptyPolicy verifies that an empty members
// map is still accepted pre-bootstrap (it performs no service ops) (FIX 3).
func (s *placementSuite) TestPlacementPreBootstrapAllowsEmptyPolicy() {
	origBootstrapped := cephIsBootstrappedFunc
	cephIsBootstrappedFunc = func(_ context.Context, _ interfaces.StateInterface) (bool, error) {
		return false, nil // not bootstrapped, but empty map is the waiting policy
	}
	defer func() { cephIsBootstrappedFunc = origBootstrapped }()

	rec, restore := withAddRemoveRecorder()
	defer restore()

	policy := types.PlacementPolicy{Mode: "reconcile", Members: map[string]types.MemberPlacement{}}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	assert.NoError(s.T(), err, "empty policy must be accepted pre-bootstrap")
	assert.Empty(s.T(), rec.adds())
	assert.Empty(s.T(), rec.removes())
}

// TestPlacementKeepOneReadinessGuard verifies that when a replacement control
// service is newly added but NOT yet viable in Ceph (not in quorum / not active),
// the old service is NOT removed — the keep-one invariant must be based on real
// Ceph viability, not stale DB/snap records (blocker fix: keep-one safety).
func (s *placementSuite) TestPlacementKeepOneReadinessGuard() {
	defer withObservedControl(map[string]map[string]bool{
		"mon": {"node-a": true},
		"mgr": {"node-a": true},
		"mds": {"node-a": true},
	})()
	rec, restore := withAddRemoveRecorder()
	defer restore()

	// Override: node-b's services are NOT ready (just placed, not yet in
	// quorum / not yet active). node-a's services ARE ready.
	origReady := controlServiceReadyFunc
	controlServiceReadyFunc = func(_ context.Context, service string, member string) (bool, error) {
		if member == "node-b" {
			return false, nil // not viable yet
		}
		return true, nil // node-a is viable
	}
	defer func() { controlServiceReadyFunc = origReady }()

	policy := types.PlacementPolicy{
		Mode: "reconcile",
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: boolPtr(false)}, // remove old
			"node-b": {Control: boolPtr(true)},  // add new (not ready)
		},
	}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	// The add succeeds; the removal is refused because node-b is not viable.
	assert.Error(s.T(), err, "must refuse to remove old control when replacement not viable")
	assert.ErrorIs(s.T(), err, ErrKeepOneInvariant)
	// node-b was added (adds recorded), but node-a was NOT removed.
	assert.NotEmpty(s.T(), rec.adds(), "replacement must be added before readiness check")
	assert.Empty(s.T(), rec.removes(), "old service must not be removed when replacement not viable")
}

// TestPlacementRemovalAllowedWhenReplacementReady verifies that when the
// replacement IS viable in Ceph, the old service IS removed (the readiness
// guard does not block valid migrations).
func (s *placementSuite) TestPlacementRemovalAllowedWhenReplacementReady() {
	defer withObservedControl(map[string]map[string]bool{
		"mon": {"node-a": true},
		"mgr": {"node-a": true},
		"mds": {"node-a": true},
	})()
	rec, restore := withAddRemoveRecorder()
	defer restore()

	// Override: both node-a and node-b are viable.
	origReady := controlServiceReadyFunc
	controlServiceReadyFunc = func(_ context.Context, _ string, _ string) (bool, error) {
		return true, nil
	}
	defer func() { controlServiceReadyFunc = origReady }()

	policy := types.PlacementPolicy{
		Mode: "reconcile",
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: boolPtr(false)}, // remove old
			"node-b": {Control: boolPtr(true)},  // add new (ready)
		},
	}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	assert.NoError(s.T(), err)
	assert.NotEmpty(s.T(), rec.adds(), "replacement must be added")
	assert.NotEmpty(s.T(), rec.removes(), "old service must be removed when replacement is viable")
}

// TestPlacementRemovesExistingNonViableTargetWhenRetainerReady verifies that
// readiness checks do not erase service-existence information. A non-viable
// control:false service is still a removal target when another viable service
// can retain the keep-one invariant.
func (s *placementSuite) TestPlacementRemovesExistingNonViableTargetWhenRetainerReady() {
	defer withObservedControl(map[string]map[string]bool{
		"mon": {"node-a": true, "node-b": true},
		"mgr": {"node-a": true, "node-b": true},
		"mds": {"node-a": true, "node-b": true},
	})()
	rec, restore := withAddRemoveRecorder()
	defer restore()

	origReady := controlServiceReadyFunc
	controlServiceReadyFunc = func(_ context.Context, _ string, member string) (bool, error) {
		return member == "node-b", nil
	}
	defer func() { controlServiceReadyFunc = origReady }()

	policy := types.PlacementPolicy{
		Mode: "reconcile",
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: boolPtr(false)}, // existing but non-viable; remove it
			"node-b": {},                        // omitted control retains viable services
		},
	}
	err := ApplyPlacement(context.Background(), s.TestStateInterface, policy)
	assert.NoError(s.T(), err)
	assert.ElementsMatch(s.T(), []string{"mon:node-a", "mgr:node-a", "mds:node-a"}, rec.removes())
}

// TestControlServiceViabilityPollsConcurrently verifies that pending
// (service, member) readiness checks run concurrently against the shared
// deadline, so no single service can starve later services of the budget (the
// Medium "shared readiness deadline" finding). With sequential polling the max
// observed concurrency would be 1; with concurrent polling it must exceed 1.
func (s *placementSuite) TestControlServiceViabilityPollsConcurrently() {
	// node-a holds all three control services as retainer candidates; node-b is
	// a pending removal target so the polling gate triggers (node-b itself is
	// not polled).
	observedControl := map[string]map[string]bool{
		"mon": {"node-a": true, "node-b": true},
		"mgr": {"node-a": true, "node-b": true},
		"mds": {"node-a": true, "node-b": true},
	}
	policy := types.PlacementPolicy{
		Members: map[string]types.MemberPlacement{
			"node-b": {Control: boolPtr(false)}, // removal target: triggers polling
		},
	}

	var inflight int64
	var maxInflight int64
	origReady := controlServiceReadyFunc
	controlServiceReadyFunc = func(_ context.Context, _ string, _ string) (bool, error) {
		cur := atomic.AddInt64(&inflight, 1)
		for {
			prev := atomic.LoadInt64(&maxInflight)
			if cur <= prev {
				break
			}
			if atomic.CompareAndSwapInt64(&maxInflight, prev, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond) // force overlap between concurrent polls
		atomic.AddInt64(&inflight, -1)
		return true, nil
	}
	s.T().Cleanup(func() { controlServiceReadyFunc = origReady })

	// Give the deadline enough headroom that the 50ms overlaps never exhaust it.
	origTimeout := controlReadinessTimeout
	controlReadinessTimeout = 5 * time.Second
	s.T().Cleanup(func() { controlReadinessTimeout = origTimeout })

	viableControl := controlServiceViability(context.Background(), observedControl, policy)

	assert.Greater(s.T(), int(atomic.LoadInt64(&maxInflight)), 1,
		"readiness checks must run concurrently; max observed concurrency was %d", maxInflight)
	for _, svc := range controlServices {
		assert.True(s.T(), viableControl[svc]["node-a"],
			"%s on node-a must remain viable (check returned ready)", svc)
	}
}

// TestControlServiceViabilitySkipsRemovalTargets verifies that members
// whose policy entry explicitly sets control:false are not polled for
// readiness: they can never count as keep-one retainers, and polling them
// would burn the shared deadline (e.g. waiting the full budget for a dead
// node that control is being migrated away from).
func (s *placementSuite) TestControlServiceViabilitySkipsRemovalTargets() {
	observedControl := map[string]map[string]bool{
		"mon": {"node-a": true, "node-b": true},
		"mgr": {"node-a": true, "node-b": true},
		"mds": {"node-a": true, "node-b": true},
	}

	var mu sync.Mutex
	polled := make(map[string]bool)
	origReady := controlServiceReadyFunc
	controlServiceReadyFunc = func(_ context.Context, _ string, member string) (bool, error) {
		mu.Lock()
		polled[member] = true
		mu.Unlock()
		return true, nil
	}
	s.T().Cleanup(func() { controlServiceReadyFunc = origReady })

	origTimeout := controlReadinessTimeout
	controlReadinessTimeout = 0
	s.T().Cleanup(func() { controlReadinessTimeout = origTimeout })

	policy := types.PlacementPolicy{
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: boolPtr(false)}, // removal target: must not be polled
			"node-b": {},                        // omitted control: retainer candidate, polled
		},
	}
	viableControl := controlServiceViability(context.Background(), observedControl, policy)

	assert.False(s.T(), polled["node-a"], "explicit removal targets must not be polled for readiness")
	assert.True(s.T(), polled["node-b"], "retainer candidates must be polled for readiness")
	for _, svc := range controlServices {
		assert.True(s.T(), viableControl[svc]["node-a"],
			"%s on node-a keeps its copied viability entry (never read for retainer counting)", svc)
	}
}

// TestControlServiceViabilitySkipsPollingWhenNoRemovals verifies the
// short-circuit when the policy has no pending control removals
// (hasRemovals=false): polling is skipped entirely and the returned viability
// map is the existence-seeded copy of observedControl. A fail-if-called stub
// proves no readiness check runs; it also returns false so a regression that
// removed the gate would flip viability entries and fail the value assertions.
func (s *placementSuite) TestControlServiceViabilitySkipsPollingWhenNoRemovals() {
	observedControl := map[string]map[string]bool{
		"mon": {"node-a": true, "node-b": true},
		"mgr": {"node-a": true, "node-b": true},
		"mds": {"node-a": true, "node-b": true},
	}

	var mu sync.Mutex
	called := false
	origReady := controlServiceReadyFunc
	controlServiceReadyFunc = func(_ context.Context, _ string, _ string) (bool, error) {
		mu.Lock()
		called = true
		mu.Unlock()
		return false, fmt.Errorf("readiness check must not run when there are no pending removals")
	}
	s.T().Cleanup(func() { controlServiceReadyFunc = origReady })

	origTimeout := controlReadinessTimeout
	controlReadinessTimeout = 0
	s.T().Cleanup(func() { controlReadinessTimeout = origTimeout })

	// No control:false anywhere: nothing to keep-one-guard.
	policy := types.PlacementPolicy{
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: boolPtr(true)},
			"node-b": {}, // omitted control: not a removal target
		},
	}
	viableControl := controlServiceViability(context.Background(), observedControl, policy)

	assert.False(s.T(), called, "readiness must not be polled when there are no pending removals")
	for _, svc := range controlServices {
		assert.True(s.T(), viableControl[svc]["node-a"], "%s on node-a must keep its existence-seeded viability", svc)
		assert.True(s.T(), viableControl[svc]["node-b"], "%s on node-b must keep its existence-seeded viability", svc)
	}
}

// TestControlServiceViabilityAllRemovalTargetsNoRetainers verifies the
// short-circuit when every observed control service sits on an explicit
// control:false member (hasRemovals=true but len(pending)==0): there are no
// retainer candidates to poll, so polling is skipped and the viability map is
// the existence-seeded copy. A fail-if-called stub proves no readiness check
// runs; it also returns false so a regression that proceeded to poll would flip
// viability entries and fail the value assertions.
func (s *placementSuite) TestControlServiceViabilityAllRemovalTargetsNoRetainers() {
	observedControl := map[string]map[string]bool{
		"mon": {"node-a": true, "node-b": true},
		"mgr": {"node-a": true, "node-b": true},
		"mds": {"node-a": true, "node-b": true},
	}

	var mu sync.Mutex
	called := false
	origReady := controlServiceReadyFunc
	controlServiceReadyFunc = func(_ context.Context, _ string, _ string) (bool, error) {
		mu.Lock()
		called = true
		mu.Unlock()
		return false, fmt.Errorf("readiness check must not run when there are no retainer candidates")
	}
	s.T().Cleanup(func() { controlServiceReadyFunc = origReady })

	origTimeout := controlReadinessTimeout
	controlReadinessTimeout = 0
	s.T().Cleanup(func() { controlReadinessTimeout = origTimeout })

	// Every observed member is an explicit removal target: nothing to poll.
	policy := types.PlacementPolicy{
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: boolPtr(false)},
			"node-b": {Control: boolPtr(false)},
		},
	}
	viableControl := controlServiceViability(context.Background(), observedControl, policy)

	assert.False(s.T(), called, "readiness must not be polled when there are no retainer candidates")
	for _, svc := range controlServices {
		assert.True(s.T(), viableControl[svc]["node-a"], "%s on node-a must keep its existence-seeded viability", svc)
		assert.True(s.T(), viableControl[svc]["node-b"], "%s on node-b must keep its existence-seeded viability", svc)
	}
}

// TestRedactSecrets verifies that redactSecrets masks realistic cephx key
// material while leaving ordinary refusal/error text intact. This is the sole
// guard against leaking key material through GET /1.0/placement, so a regex
// regression would leak keys with nothing to catch it.
//
// Note: the short 'AQABfakekey==' fixture used elsewhere in the test suite is
// intentionally below the {20,} character threshold of secretPattern and is
// NOT a valid positive-match sample — it must not be used to verify redaction.
func (s *placementSuite) TestRedactSecrets() {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "realistic cephx key replaced",
			input:    "AQBHrVFabc123DEFghi456JKLmno789PQRstuvw==",
			expected: "AQ****REDACTED****==",
		},
		{
			name:     "key embedded in error text masked, rest preserved",
			input:    "failed to create keyring: AQBHrVFabc123DEFghi456JKLmno789PQRstuvw== for client.admin",
			expected: "failed to create keyring: AQ****REDACTED****== for client.admin",
		},
		{
			name:     "non-secret refusal text passes through unchanged",
			input:    "keep-one invariant: refused to remove last mon on node-a",
			expected: "keep-one invariant: refused to remove last mon on node-a",
		},
		{
			name:     "short fakekey fixture below threshold is not redacted",
			input:    "AQABfakekey==",
			expected: "AQABfakekey==",
		},
		{
			name:     "empty string unchanged",
			input:    "",
			expected: "",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			assert.Equal(s.T(), tc.expected, redactSecrets(tc.input))
		})
	}
}
