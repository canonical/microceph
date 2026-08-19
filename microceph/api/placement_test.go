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

// placementApplyRecorder captures the policy and context handed from the HTTP
// handler to the placement package's single orchestration entry point.
type placementApplyRecorder struct {
	calls            int
	policy           types.PlacementPolicy
	contextCancelled bool
}

// stubPlacementApply replaces ApplyPlacementPolicy for handler tests and
// restores it on cleanup. Validate-store-reconcile ordering is tested in the
// ceph package, where those phases are deliberately package-private.
func stubPlacementApply(t *testing.T, apply func(context.Context, types.PlacementPolicy) error) *placementApplyRecorder {
	t.Helper()
	rec := &placementApplyRecorder{}
	origApply := ceph.ApplyPlacementPolicyFunc
	ceph.ApplyPlacementPolicyFunc = func(ctx context.Context, _ interfaces.StateInterface, policy types.PlacementPolicy) error {
		rec.calls++
		rec.policy = policy
		select {
		case <-ctx.Done():
			rec.contextCancelled = true
		default:
		}
		if apply != nil {
			return apply(ctx, policy)
		}
		return nil
	}
	t.Cleanup(func() { ceph.ApplyPlacementPolicyFunc = origApply })
	return rec
}

// TestPlacementPutSuccess verifies that cmdPlacementPut decodes the policy,
// passes it to the placement package's orchestration entry point, releases the
// apply lock, and returns success.
func TestPlacementPutSuccess(t *testing.T) {
	unlockCalls := stubPlacementApplyLock(t)
	rec := stubPlacementApply(t, nil)

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, rec.calls)
	control := true
	assert.Equal(t, types.PlacementPolicy{
		Mode: types.PlacementModeReconcile,
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: &control},
		},
	}, rec.policy)
	assert.Equal(t, 1, *unlockCalls, "the apply lock must be released exactly once")
}

// TestPlacementPutValidationFailureReturns400 verifies that a validation
// sentinel returned by the placement orchestration maps to HTTP 400.
func TestPlacementPutValidationFailureReturns400(t *testing.T) {
	stubPlacementApplyLock(t)
	rec := stubPlacementApply(t, func(context.Context, types.PlacementPolicy) error {
		return fmt.Errorf("%w: bad-node", ceph.ErrUnknownPlacementMember)
	})

	body := `{"mode":"reconcile","members":{"bad-node":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, 1, rec.calls)
	assert.Equal(t, http.StatusBadRequest, w.Code, "client-side placement error must return 400")
}

// TestPlacementPutPreBootstrapReturns400 verifies that the
// ErrCephNotBootstrapped sentinel maps to HTTP 400 rather than 500.
func TestPlacementPutPreBootstrapReturns400(t *testing.T) {
	stubPlacementApplyLock(t)
	stubPlacementApply(t, func(context.Context, types.PlacementPolicy) error {
		return fmt.Errorf("%w: run bootstrap-ceph first", ceph.ErrCephNotBootstrapped)
	})

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, "pre-bootstrap rejection must return 400 not 500")
}

// TestPlacementPutKeepOneReturns400 verifies that the ErrKeepOneInvariant
// sentinel maps to HTTP 400 (not 500).
func TestPlacementPutKeepOneReturns400(t *testing.T) {
	stubPlacementApplyLock(t)
	stubPlacementApply(t, func(context.Context, types.PlacementPolicy) error {
		return fmt.Errorf("%w: refused to remove last mon on node-a", ceph.ErrKeepOneInvariant)
	})

	body := `{"mode":"reconcile","members":{"node-a":{"control":false}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, "keep-one refusal must return 400 not 500")
}

// TestPlacementPutServerErrorReturns500 verifies that a non-client-side apply
// error falls through to SmartError as HTTP 500 and still releases the lock.
func TestPlacementPutServerErrorReturns500(t *testing.T) {
	unlockCalls := stubPlacementApplyLock(t)
	stubPlacementApply(t, func(context.Context, types.PlacementPolicy) error {
		return errors.New("database connection refused")
	})

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code, "server-side error must return 500")
	assert.Equal(t, 1, *unlockCalls, "the apply lock must be released even when the apply fails")
}

// TestPlacementPutContextDetached verifies that cmdPlacementPut passes the
// orchestration entry point a context detached from request cancellation. This
// prevents context-canceled failures during multi-minute readiness polling.
func TestPlacementPutContextDetached(t *testing.T) {
	stubPlacementApplyLock(t)
	rec := stubPlacementApply(t, nil)

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
	assert.False(t, rec.contextCancelled,
		"placement apply must receive a context detached from the request's cancellation")
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
// mode is rejected with BadRequest before any lock or placement apply, so a
// future mode sent to an older snap fails loudly.
func TestPlacementPutUnknownModeRejected(t *testing.T) {
	unlockCalls := stubPlacementApplyLock(t)
	rec := stubPlacementApply(t, nil)

	body := `{"mode":"dry-run","members":{"node-a":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, "unknown mode must be rejected with 400")
	assert.Equal(t, 0, rec.calls, "unknown mode must not apply a placement policy")
	assert.Equal(t, 0, *unlockCalls, "unknown mode must be rejected before the lock is taken")
}

// TestPlacementPutReconcileModeAccepted verifies that an explicit "reconcile"
// mode is accepted (200).
func TestPlacementPutReconcileModeAccepted(t *testing.T) {
	stubPlacementApplyLock(t)
	stubPlacementApply(t, nil)

	body := `{"mode":"reconcile","members":{}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "body %s must be accepted", body)
}

// TestPlacementPutWaitingPolicyApplied verifies that the CE142 waiting policy
// (an empty members map) is passed to the placement orchestration rather than
// treated as a no-op request.
func TestPlacementPutWaitingPolicyApplied(t *testing.T) {
	stubPlacementApplyLock(t)
	rec := stubPlacementApply(t, nil)

	body := `{"mode":"reconcile","members":{}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, rec.calls)
	assert.Equal(t, types.PlacementPolicy{
		Mode:    types.PlacementModeReconcile,
		Members: map[string]types.MemberPlacement{},
	}, rec.policy)
}

// TestPlacementPutMissingModeRejected verifies that an omitted mode is rejected
// with 400 (mode is required, not defaulted to reconcile) before any engine or
// lock interaction.
func TestPlacementPutMissingModeRejected(t *testing.T) {
	unlockCalls := stubPlacementApplyLock(t)
	rec := stubPlacementApply(t, nil)

	body := `{"members":{}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, "a missing mode must be rejected with 400")
	assert.Equal(t, 0, rec.calls, "a missing mode must not apply a placement policy")
	assert.Equal(t, 0, *unlockCalls, "a missing mode must be rejected before the lock is taken")
}

// TestPlacementPutLockHeldReturnsRetryableError verifies that when another
// placement apply holds the cluster-wide lock, the handler returns an error
// without applying the policy or releasing the other holder's lock.
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
	rec := stubPlacementApply(t, nil)

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(w, req)

	assert.Equal(t, http.StatusConflict, w.Code, "a held apply lock must fail the PUT with 409 Conflict (retryable)")
	assert.Equal(t, 0, rec.calls, "no placement work may run while another apply holds the lock")
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
	stubPlacementApply(t, func(context.Context, types.PlacementPolicy) error {
		return errors.New("apply blew up")
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
