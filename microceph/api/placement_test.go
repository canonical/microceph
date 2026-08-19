package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPlacementApplyLock replaces the placement apply lock/unlock functions
// with no-op stubs for handler tests that exercise the apply path, restoring
// them on cleanup. It returns a pointer to a counter of unlock calls so tests
// can assert the lock is always released.
func stubPlacementApplyLock(t *testing.T) *int {
	t.Helper()
	unlockCalls := 0
	origLock := ceph.LockPlacementApplyFunc
	origUnlock := ceph.UnlockPlacementApplyFunc
	ceph.LockPlacementApplyFunc = func(_ context.Context, _ interfaces.StateInterface) (int64, error) {
		return 1, nil
	}
	ceph.UnlockPlacementApplyFunc = func(_ context.Context, _ interfaces.StateInterface, _ int64) error {
		unlockCalls++
		return nil
	}
	t.Cleanup(func() {
		ceph.LockPlacementApplyFunc = origLock
		ceph.UnlockPlacementApplyFunc = origUnlock
	})
	return &unlockCalls
}

// placementRecorder captures how cmdPlacementPut drove the placement engine:
// which phases ran and in what order, the policy handed to the store, and every
// refusal written. Phase names recorded in order are "validate", "store",
// "reconcile", and "refusal:<reason>".
type placementRecorder struct {
	order        []string
	storedPolicy types.PlacementPolicy
	refusals     []string
}

// ran reports whether the named phase executed.
func (r *placementRecorder) ran(phase string) bool {
	for _, p := range r.order {
		if p == phase {
			return true
		}
	}
	return false
}

// placementHooks lets a test fail one phase of the handler's validate-store-
// reconcile sequence. A nil hook makes that phase a recorded success.
type placementHooks struct {
	validate  func(context.Context) error
	store     func(context.Context) error
	reconcile func(context.Context) error
	refusal   func(context.Context, string) error
}

// stubPlacementEngine replaces the three engine phases cmdPlacementPut drives,
// plus the refusal writer, and restores them on cleanup. The returned recorder
// exposes the resulting call sequence so tests can assert not just that a phase
// ran but that it ran in the right order -- notably that the desired policy is
// stored before reconciliation, not after it.
func stubPlacementEngine(t *testing.T, h placementHooks) *placementRecorder {
	t.Helper()
	rec := &placementRecorder{}

	origValidate := ceph.ValidatePlacementFunc
	origStore := ceph.StorePlacementPolicyFunc
	origReconcile := ceph.ReconcilePlacementFunc
	origRefusal := ceph.SetPlacementRefusalFunc

	ceph.ValidatePlacementFunc = func(ctx context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		rec.order = append(rec.order, "validate")
		if h.validate != nil {
			return h.validate(ctx)
		}
		return nil
	}
	ceph.StorePlacementPolicyFunc = func(ctx context.Context, _ interfaces.StateInterface, p types.PlacementPolicy) error {
		rec.order = append(rec.order, "store")
		rec.storedPolicy = p
		if h.store != nil {
			return h.store(ctx)
		}
		return nil
	}
	ceph.ReconcilePlacementFunc = func(ctx context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		rec.order = append(rec.order, "reconcile")
		if h.reconcile != nil {
			return h.reconcile(ctx)
		}
		return nil
	}
	ceph.SetPlacementRefusalFunc = func(ctx context.Context, _ interfaces.StateInterface, reason string) error {
		rec.order = append(rec.order, "refusal:"+reason)
		rec.refusals = append(rec.refusals, reason)
		if h.refusal != nil {
			return h.refusal(ctx, reason)
		}
		return nil
	}

	t.Cleanup(func() {
		ceph.ValidatePlacementFunc = origValidate
		ceph.StorePlacementPolicyFunc = origStore
		ceph.ReconcilePlacementFunc = origReconcile
		ceph.SetPlacementRefusalFunc = origRefusal
	})
	return rec
}

// TestPlacementPutSuccess verifies that cmdPlacementPut decodes the policy and
// drives the three phases in order -- validate, store, reconcile -- then clears
// any stale refusal, releases the apply lock, and returns success.
func TestPlacementPutSuccess(t *testing.T) {
	unlockCalls := stubPlacementApplyLock(t)
	rec := stubPlacementEngine(t, placementHooks{})

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{"validate", "store", "reconcile", "refusal:"}, rec.order,
		"the handler must validate, then persist the desired policy, then reconcile")
	assert.Equal(t, 1, *unlockCalls, "the apply lock must be released exactly once")
}

// TestPlacementPutValidationFailureNotStored (N3) verifies that a policy
// rejected in the validate phase (e.g. unknown member) never reaches the store:
// the previously stored desired policy must survive a bad request untouched, so
// an invalid PUT cannot revoke the grants of the policy already in effect.
// Reconciliation is skipped and the response is HTTP 400 because the error is a
// client-side sentinel.
func TestPlacementPutValidationFailureNotStored(t *testing.T) {
	stubPlacementApplyLock(t)
	rec := stubPlacementEngine(t, placementHooks{
		validate: func(context.Context) error {
			return fmt.Errorf("%w: bad-node", ceph.ErrUnknownPlacementMember)
		},
	})

	body := `{"mode":"reconcile","members":{"bad-node":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.False(t, rec.ran("store"), "a policy that fails validation must not replace the stored desired policy")
	assert.False(t, rec.ran("reconcile"), "reconciliation must not run for a policy that failed validation")
	require.Len(t, rec.refusals, 1, "the rejection reason must be recorded")
	assert.Contains(t, rec.refusals[0], "bad-node")
	assert.Equal(t, http.StatusBadRequest, w.Code, "client-side placement error must return 400")
}

// TestPlacementPutPreBootstrapReturns400 verifies that the ErrCephNotBootstrapped
// sentinel maps to HTTP 400 (not 500) and that the policy is not stored.
func TestPlacementPutPreBootstrapReturns400(t *testing.T) {
	stubPlacementApplyLock(t)
	rec := stubPlacementEngine(t, placementHooks{
		validate: func(context.Context) error {
			return fmt.Errorf("%w: run bootstrap-ceph first", ceph.ErrCephNotBootstrapped)
		},
	})

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, "pre-bootstrap rejection must return 400 not 500")
	assert.False(t, rec.ran("store"), "a pre-bootstrap rejection must not replace the stored desired policy")
}

// TestPlacementPutKeepOneReturns400 verifies that the ErrKeepOneInvariant
// sentinel maps to HTTP 400 (not 500).
func TestPlacementPutKeepOneReturns400(t *testing.T) {
	stubPlacementApplyLock(t)
	stubPlacementEngine(t, placementHooks{
		reconcile: func(context.Context) error {
			return fmt.Errorf("%w: refused to remove last mon on node-a", ceph.ErrKeepOneInvariant)
		},
	})

	body := `{"mode":"reconcile","members":{"node-a":{"control":false}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, "keep-one refusal must return 400 not 500")
}

// TestPlacementPutKeepOneStoresPolicyAndRefusal verifies that a keep-one
// refusal leaves the requested snapshot as the desired policy: it was persisted
// before reconciliation ran, so GET /placement reports the desired-vs-observed
// gap with last_refusal explaining it. The response is HTTP 400.
func TestPlacementPutKeepOneStoresPolicyAndRefusal(t *testing.T) {
	stubPlacementApplyLock(t)
	rec := stubPlacementEngine(t, placementHooks{
		reconcile: func(context.Context) error {
			return fmt.Errorf("%w: refused to remove last mon on node-a", ceph.ErrKeepOneInvariant)
		},
	})

	body := `{"mode":"reconcile","members":{"node-a":{"control":false},"node-b":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, "keep-one refusal must return 400 not 500")
	require.Len(t, rec.refusals, 1, "the refusal reason must be recorded")
	assert.Contains(t, rec.refusals[0], "keep-one invariant")
	assert.Equal(t, []string{"validate", "store", "reconcile", "refusal:" + rec.refusals[0]}, rec.order,
		"the policy must be stored before reconciliation, so a refusal cannot discard it")

	// The stored policy must be the submitted snapshot verbatim.
	ctrlFalse := false
	ctrlTrue := true
	assert.Equal(t, types.PlacementPolicy{
		Mode: types.PlacementModeReconcile,
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: &ctrlFalse},
			"node-b": {Control: &ctrlTrue},
		},
	}, rec.storedPolicy)
}

// TestPlacementPutReconcileFailureStoresDesiredPolicy verifies that a
// mid-reconcile server-side failure still leaves the submitted snapshot as the
// canonical desired policy. Persisting before reconciling is what makes the
// failure observable: the operator's intent survives as desired state and
// last_refusal records what stopped it, instead of the superseded policy
// silently remaining in effect as though the PUT never happened.
func TestPlacementPutReconcileFailureStoresDesiredPolicy(t *testing.T) {
	stubPlacementApplyLock(t)
	rec := stubPlacementEngine(t, placementHooks{
		reconcile: func(context.Context) error {
			return errors.New("failed to add mon on node-b: connection refused")
		},
	})

	body := `{"mode":"reconcile","members":{"node-a":{"control":true},"node-b":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code, "a mid-reconcile failure is a server fault")
	assert.True(t, rec.ran("store"), "the desired policy must survive a reconciliation failure")
	ctrlTrue := true
	assert.Equal(t, types.PlacementPolicy{
		Mode: types.PlacementModeReconcile,
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: &ctrlTrue},
			"node-b": {Control: &ctrlTrue},
		},
	}, rec.storedPolicy, "the whole submitted snapshot must be stored, not the part that converged")
	require.Len(t, rec.refusals, 1, "the failure reason must be recorded alongside the desired policy")
	assert.Contains(t, rec.refusals[0], "connection refused")
}

// TestPlacementPutStoreFailureSkipsReconcile verifies that when the desired
// policy cannot be persisted the handler stops before reconciling: mutating
// services with no durable record of the intent behind them is exactly the gap
// the store-before-reconcile ordering exists to close. The failure reason is
// still recorded and the response is HTTP 500.
func TestPlacementPutStoreFailureSkipsReconcile(t *testing.T) {
	unlockCalls := stubPlacementApplyLock(t)
	rec := stubPlacementEngine(t, placementHooks{
		store: func(context.Context) error {
			return errors.New("database unavailable")
		},
	})

	body := `{"mode":"reconcile","members":{"node-a":{"control":false},"node-b":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code, "an unpersistable policy must return 500")
	assert.False(t, rec.ran("reconcile"), "services must not be mutated when the desired policy could not be stored")
	require.Len(t, rec.refusals, 1, "the store failure must be recorded")
	assert.Contains(t, rec.refusals[0], "database unavailable")
	assert.Equal(t, 1, *unlockCalls, "the apply lock must be released when the store fails")
}

// TestPlacementPutServerErrorReturns500 verifies that a non-client-side
// reconcile error (e.g. DB failure) does NOT map to 400 but falls through to
// SmartError which returns 500, and that the apply lock is still released.
func TestPlacementPutServerErrorReturns500(t *testing.T) {
	unlockCalls := stubPlacementApplyLock(t)
	stubPlacementEngine(t, placementHooks{
		reconcile: func(context.Context) error {
			return errors.New("database connection refused")
		},
	})

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code, "server-side error must return 500")
	assert.Equal(t, 1, *unlockCalls, "the apply lock must be released even when the apply fails")
}

// TestPlacementPutContextDetached verifies that cmdPlacementPut uses a context
// detached from the request's cancellation: even when the request context is
// cancelled, every engine phase receives a non-cancelled context. This prevents
// the "context canceled" error during multi-minute readiness polling.
func TestPlacementPutContextDetached(t *testing.T) {
	stubPlacementApplyLock(t)
	cancelled := map[string]bool{}
	noteCancelled := func(phase string) func(context.Context) error {
		return func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				cancelled[phase] = true
			default:
				cancelled[phase] = false
			}
			return nil
		}
	}
	stubPlacementEngine(t, placementHooks{
		validate:  noteCancelled("validate"),
		store:     noteCancelled("store"),
		reconcile: noteCancelled("reconcile"),
	})

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	// Cancel the request context before the handler runs.
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "handler must succeed despite cancelled request context")
	assert.Equal(t, map[string]bool{"validate": false, "store": false, "reconcile": false}, cancelled,
		"every engine phase must receive a context detached from the request's cancellation")
}

// TestPlacementPutBadJSON verifies that malformed JSON returns BadRequest.
func TestPlacementPutBadJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader("{bad json"))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestPlacementPutUnknownModeRejected verifies that a policy with an unknown
// mode is rejected with BadRequest before any lock, validate, store, or
// reconcile happens, so a future mode (e.g. dry-run) sent to an older snap
// fails loudly instead of being silently applied as a reconcile.
func TestPlacementPutUnknownModeRejected(t *testing.T) {
	unlockCalls := stubPlacementApplyLock(t)
	rec := stubPlacementEngine(t, placementHooks{})

	body := `{"mode":"dry-run","members":{"node-a":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, "unknown mode must be rejected with 400")
	assert.Empty(t, rec.order, "unknown mode must not touch the placement engine or the policy store")
	assert.Equal(t, 0, *unlockCalls, "unknown mode must be rejected before the lock is taken")
}

// TestPlacementPutReconcileModeAccepted verifies that an explicit "reconcile"
// mode is accepted (200).
func TestPlacementPutReconcileModeAccepted(t *testing.T) {
	stubPlacementApplyLock(t)
	stubPlacementEngine(t, placementHooks{})

	body := `{"mode":"reconcile","members":{}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "body %s must be accepted", body)
}

// TestPlacementPutWaitingPolicyStored verifies that the CE142 waiting policy
// (an empty members map) is persisted as the desired policy rather than treated
// as a no-op request. Storing it is the point: an empty members map is an empty
// storage allow-list, which is how the charm withholds OSD enrollment while it
// has no valid role assignments to publish.
func TestPlacementPutWaitingPolicyStored(t *testing.T) {
	stubPlacementApplyLock(t)
	rec := stubPlacementEngine(t, placementHooks{})

	body := `{"mode":"reconcile","members":{}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, rec.ran("store"), "the waiting policy must be stored as the desired policy")
	assert.Equal(t, types.PlacementPolicy{
		Mode:    types.PlacementModeReconcile,
		Members: map[string]types.MemberPlacement{},
	}, rec.storedPolicy)
}

// TestPlacementPutMissingModeRejected verifies that an omitted mode is rejected
// with 400 (mode is required, not defaulted to reconcile) before any engine or
// lock interaction.
func TestPlacementPutMissingModeRejected(t *testing.T) {
	unlockCalls := stubPlacementApplyLock(t)
	rec := stubPlacementEngine(t, placementHooks{})

	body := `{"members":{}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, "a missing mode must be rejected with 400")
	assert.Empty(t, rec.order, "a missing mode must not touch the placement engine or the policy store")
	assert.Equal(t, 0, *unlockCalls, "a missing mode must be rejected before the lock is taken")
}

// TestPlacementPutLockHeldReturnsRetryableError verifies that when another
// placement apply holds the cluster-wide lock, the handler returns an error
// without validating, storing, reconciling, recording a refusal, or releasing
// the other holder's lock.
func TestPlacementPutLockHeldReturnsRetryableError(t *testing.T) {
	unlockCalled := false
	origLock := ceph.LockPlacementApplyFunc
	origUnlock := ceph.UnlockPlacementApplyFunc
	ceph.LockPlacementApplyFunc = func(_ context.Context, _ interfaces.StateInterface) (int64, error) {
		return 0, fmt.Errorf("%w: retry after the current apply completes", ceph.ErrPlacementApplyInProgress)
	}
	ceph.UnlockPlacementApplyFunc = func(_ context.Context, _ interfaces.StateInterface, _ int64) error {
		unlockCalled = true
		return nil
	}
	t.Cleanup(func() {
		ceph.LockPlacementApplyFunc = origLock
		ceph.UnlockPlacementApplyFunc = origUnlock
	})
	rec := stubPlacementEngine(t, placementHooks{})

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusConflict, w.Code, "a held apply lock must fail the PUT with 409 Conflict (retryable)")
	assert.Empty(t, rec.order, "no placement work may run while another apply holds the lock")
	assert.Empty(t, rec.refusals, "a lock conflict is not a policy refusal and must not overwrite last_refusal")
	assert.False(t, unlockCalled, "the handler must not release a lock it failed to acquire")
}

// TestPlacementPutLockReleasedWithAcquiredToken verifies that the handler
// releases the apply lock with the exact token it acquired, also when
// reconciliation fails.
func TestPlacementPutLockReleasedWithAcquiredToken(t *testing.T) {
	const token = int64(42)
	var releasedToken int64
	origLock := ceph.LockPlacementApplyFunc
	origUnlock := ceph.UnlockPlacementApplyFunc
	ceph.LockPlacementApplyFunc = func(_ context.Context, _ interfaces.StateInterface) (int64, error) {
		return token, nil
	}
	ceph.UnlockPlacementApplyFunc = func(_ context.Context, _ interfaces.StateInterface, tok int64) error {
		releasedToken = tok
		return nil
	}
	t.Cleanup(func() {
		ceph.LockPlacementApplyFunc = origLock
		ceph.UnlockPlacementApplyFunc = origUnlock
	})
	stubPlacementEngine(t, placementHooks{
		reconcile: func(context.Context) error { return errors.New("apply blew up") },
	})

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, token, releasedToken, "the lock must be released with the acquired token")
}

// TestPlacementDeleteSuccess verifies that cmdPlacementDelete acquires the
// apply lock, calls ClearPlacementPolicy, releases the lock, and returns
// success.
func TestPlacementDeleteSuccess(t *testing.T) {
	unlockCalls := stubPlacementApplyLock(t)
	clearCalled := false
	origClear := ceph.ClearPlacementPolicyFunc
	ceph.ClearPlacementPolicyFunc = func(_ context.Context, _ interfaces.StateInterface) error {
		clearCalled = true
		return nil
	}
	defer func() { ceph.ClearPlacementPolicyFunc = origClear }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/1.0/placement", nil)

	resp := cmdPlacementDelete(nil, req)
	_ = resp.Render(rec, req)

	assert.True(t, clearCalled)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, *unlockCalls, "the apply lock must be released exactly once")
}

// TestPlacementDeleteLockHeldReturnsRetryableError verifies that DELETE
// /placement acquires the apply lock and returns a retryable error (not 200)
// when another apply holds it, without calling ClearPlacementPolicy.
func TestPlacementDeleteLockHeldReturnsRetryableError(t *testing.T) {
	clearCalled := false
	unlockCalled := false
	origLock := ceph.LockPlacementApplyFunc
	origUnlock := ceph.UnlockPlacementApplyFunc
	origClear := ceph.ClearPlacementPolicyFunc
	ceph.LockPlacementApplyFunc = func(_ context.Context, _ interfaces.StateInterface) (int64, error) {
		return 0, fmt.Errorf("%w: retry after the current apply completes", ceph.ErrPlacementApplyInProgress)
	}
	ceph.UnlockPlacementApplyFunc = func(_ context.Context, _ interfaces.StateInterface, _ int64) error {
		unlockCalled = true
		return nil
	}
	ceph.ClearPlacementPolicyFunc = func(_ context.Context, _ interfaces.StateInterface) error {
		clearCalled = true
		return nil
	}
	defer func() {
		ceph.LockPlacementApplyFunc = origLock
		ceph.UnlockPlacementApplyFunc = origUnlock
		ceph.ClearPlacementPolicyFunc = origClear
	}()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/1.0/placement", nil)

	resp := cmdPlacementDelete(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code, "a held apply lock must fail the DELETE with 409 Conflict (retryable)")
	assert.False(t, clearCalled, "ClearPlacementPolicy must not run while another apply holds the lock")
	assert.False(t, unlockCalled, "the handler must not release a lock it failed to acquire")
}

// TestPlacementPutUnknownFieldRejected verifies that unknown top-level keys in
// the placement policy body are rejected with BadRequest (M1). Without
// DisallowUnknownFields a typoed key like "member" (instead of "members")
// would decode to an empty no-op policy that silently overwrites the active
// one.
func TestPlacementPutUnknownFieldRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(`{"member":{"node-a":{"control":true}}}`))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "unknown field must be rejected with 400")
}

// TestCephBootstrapPutUnknownFieldRejected verifies that unknown keys in the
// Ceph bootstrap request body are rejected with BadRequest (M1).
func TestCephBootstrapPutUnknownFieldRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/ceph/bootstrap", strings.NewReader(`{"target":"node-b","force_bootstrap":true}`))

	resp := cmdCephBootstrapPut(newTestState("node-b"), req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "unknown field must be rejected with 400")
}

// TestPlacementGetSuccess verifies that cmdPlacementGet returns placement status.
func TestPlacementGetSuccess(t *testing.T) {
	origGet := ceph.GetPlacementStatusFunc
	ceph.GetPlacementStatusFunc = func(_ context.Context, _ interfaces.StateInterface) (*types.PlacementStatus, error) {
		return &types.PlacementStatus{
			Active:         true,
			BootstrapState: "bootstrapped",
		}, nil
	}
	defer func() { ceph.GetPlacementStatusFunc = origGet }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/1.0/placement", nil)

	resp := cmdPlacementGet(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var raw struct {
		Metadata types.PlacementStatus `json:"metadata"`
	}
	err := json.NewDecoder(rec.Body).Decode(&raw)
	require.NoError(t, err)
	assert.True(t, raw.Metadata.Active)
	assert.Equal(t, "bootstrapped", raw.Metadata.BootstrapState)
}

// newTestState creates a mcTypes.State with the given cluster name for API
// handler tests that need s.Name().
func newTestState(clusterName string) mcTypes.State {
	return &mocks.MockState{ClusterName: clusterName}
}

// TestCephBootstrapPutTargetFromBody verifies that the target is read from the
// JSON body and the bootstrap function is called, when the request reaches the
// correct target member (s.Name() == target).
func TestCephBootstrapPutTargetFromBody(t *testing.T) {
	var capturedTarget string
	var capturedBd common.BootstrapConfig
	origBootstrap := ceph.BootstrapCephFunc
	ceph.BootstrapCephFunc = func(_ context.Context, _ interfaces.StateInterface, target string, bd common.BootstrapConfig, _ bool) error {
		capturedTarget = target
		capturedBd = bd
		return nil
	}
	defer func() { ceph.BootstrapCephFunc = origBootstrap }()

	body := `{"target":"node-b","mon_ip":"10.0.0.1"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/ceph/bootstrap", strings.NewReader(body))

	resp := cmdCephBootstrapPut(newTestState("node-b"), req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "node-b", capturedTarget)
	assert.Equal(t, "10.0.0.1", capturedBd.MonIp)
}

// TestCephBootstrapPutTargetFromQuery verifies that the target is read from the
// query param when the body is empty (EOF), when the request reaches the
// correct target member.
func TestCephBootstrapPutTargetFromQuery(t *testing.T) {
	var capturedTarget string
	origBootstrap := ceph.BootstrapCephFunc
	ceph.BootstrapCephFunc = func(_ context.Context, _ interfaces.StateInterface, target string, _ common.BootstrapConfig, _ bool) error {
		capturedTarget = target
		return nil
	}
	defer func() { ceph.BootstrapCephFunc = origBootstrap }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/ceph/bootstrap?target=node-c", nil)

	resp := cmdCephBootstrapPut(newTestState("node-c"), req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "node-c", capturedTarget)
}

// TestCephBootstrapPutNoTarget verifies that missing target returns BadRequest.
func TestCephBootstrapPutNoTarget(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/ceph/bootstrap", nil)

	resp := cmdCephBootstrapPut(newTestState("node-a"), req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestCephBootstrapPutTargetMismatch verifies the defensive guard: a direct
// caller sending {"target":"node-b"} to node-a (s.Name() == "node-a") is
// rejected with BadRequest because the bootstrap would run on the wrong member.
func TestCephBootstrapPutTargetMismatch(t *testing.T) {
	bootstrapCalled := false
	origBootstrap := ceph.BootstrapCephFunc
	ceph.BootstrapCephFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig, _ bool) error {
		bootstrapCalled = true
		return nil
	}
	defer func() { ceph.BootstrapCephFunc = origBootstrap }()

	body := `{"target":"node-b"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/ceph/bootstrap", strings.NewReader(body))

	resp := cmdCephBootstrapPut(newTestState("node-a"), req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "mismatched target must be rejected")
	assert.False(t, bootstrapCalled, "BootstrapCeph must not run on the wrong member")
}

// TestCephBootstrapPutInProgress verifies that ErrCephBootstrapInProgress is
// NOT a client-side operator error (400), but a retryable in-progress
// condition: it maps to HTTP 409 Conflict so an orchestrator can distinguish
// it from a genuine server fault (500).
func TestCephBootstrapPutInProgress(t *testing.T) {
	origBootstrap := ceph.BootstrapCephFunc
	ceph.BootstrapCephFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig, _ bool) error {
		return ceph.ErrCephBootstrapInProgress
	}
	defer func() { ceph.BootstrapCephFunc = origBootstrap }()

	body := `{"target":"node-b"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/ceph/bootstrap", strings.NewReader(body))

	resp := cmdCephBootstrapPut(newTestState("node-b"), req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code, "in-progress is a retryable condition, mapped to 409 Conflict")
}

// TestCephBootstrapPutAlreadyBootstrapped verifies that already-bootstrapped
// (nil from BootstrapCeph) maps to success.
func TestCephBootstrapPutAlreadyBootstrapped(t *testing.T) {
	origBootstrap := ceph.BootstrapCephFunc
	ceph.BootstrapCephFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig, _ bool) error {
		return nil // no-op success
	}
	defer func() { ceph.BootstrapCephFunc = origBootstrap }()

	body := `{"target":"node-b"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/ceph/bootstrap", strings.NewReader(body))

	resp := cmdCephBootstrapPut(newTestState("node-b"), req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestCephBootstrapPutUnknownMember verifies that unknown-target errors map
// to HTTP 400 (BadRequest), mirroring cmdPlacementPut's client-side error
// mapping.
func TestCephBootstrapPutUnknownMember(t *testing.T) {
	origBootstrap := ceph.BootstrapCephFunc
	ceph.BootstrapCephFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig, _ bool) error {
		return ceph.ErrUnknownBootstrapTarget
	}
	defer func() { ceph.BootstrapCephFunc = origBootstrap }()

	body := `{"target":"node-b"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/ceph/bootstrap", strings.NewReader(body))

	resp := cmdCephBootstrapPut(newTestState("node-b"), req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "unknown bootstrap target is an operator error and must return 400")
}

// TestCephBootstrapPutMalformedJSON verifies that non-EOF JSON decode errors
// return BadRequest (M1).
func TestCephBootstrapPutMalformedJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/ceph/bootstrap?target=node-b", strings.NewReader("{bad json"))

	resp := cmdCephBootstrapPut(newTestState("node-b"), req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
