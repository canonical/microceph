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

// TestPlacementPutSuccess verifies that cmdPlacementPut decodes the policy,
// applies it, stores it, releases the apply lock, and returns success.
func TestPlacementPutSuccess(t *testing.T) {
	unlockCalls := stubPlacementApplyLock(t)
	applyCalled := false
	storeCalled := false
	origApply := ceph.ApplyPlacementFunc
	origStore := ceph.StorePlacementPolicyFunc
	origRefusal := ceph.SetPlacementRefusalFunc
	ceph.ApplyPlacementFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		applyCalled = true
		return nil
	}
	ceph.StorePlacementPolicyFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		storeCalled = true
		return nil
	}
	ceph.SetPlacementRefusalFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) error {
		return nil
	}
	defer func() {
		ceph.ApplyPlacementFunc = origApply
		ceph.StorePlacementPolicyFunc = origStore
		ceph.SetPlacementRefusalFunc = origRefusal
	}()

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, applyCalled)
	assert.True(t, storeCalled)
	assert.Equal(t, 1, *unlockCalls, "the apply lock must be released exactly once")
}

// TestPlacementPutApplyFailureNotStored (N3) verifies that when ApplyPlacement
// fails (e.g. unknown member), StorePlacementPolicy is NOT called and the
// response is HTTP 400 (BadRequest) because the error is a client-side sentinel.
func TestPlacementPutApplyFailureNotStored(t *testing.T) {
	stubPlacementApplyLock(t)
	storeCalled := false
	refusalCalled := false
	var refusalReason string
	origApply := ceph.ApplyPlacementFunc
	origStore := ceph.StorePlacementPolicyFunc
	origRefusal := ceph.SetPlacementRefusalFunc
	ceph.ApplyPlacementFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		return fmt.Errorf("%w: bad-node", ceph.ErrUnknownPlacementMember)
	}
	ceph.StorePlacementPolicyFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		storeCalled = true
		return nil
	}
	ceph.SetPlacementRefusalFunc = func(_ context.Context, _ interfaces.StateInterface, reason string) error {
		refusalCalled = true
		refusalReason = reason
		return nil
	}
	defer func() {
		ceph.ApplyPlacementFunc = origApply
		ceph.StorePlacementPolicyFunc = origStore
		ceph.SetPlacementRefusalFunc = origRefusal
	}()

	body := `{"mode":"reconcile","members":{"bad-node":{"control":true}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.False(t, storeCalled, "StorePlacementPolicy must not be called when Apply fails")
	assert.True(t, refusalCalled, "SetPlacementRefusal must be called when Apply fails")
	assert.Contains(t, refusalReason, "bad-node")
	assert.Equal(t, http.StatusBadRequest, rec.Code, "client-side placement error must return 400")
}

// TestPlacementPutPreBootstrapReturns400 verifies that the ErrCephNotBootstrapped
// sentinel maps to HTTP 400 (not 500).
func TestPlacementPutPreBootstrapReturns400(t *testing.T) {
	stubPlacementApplyLock(t)
	origApply := ceph.ApplyPlacementFunc
	origStore := ceph.StorePlacementPolicyFunc
	origRefusal := ceph.SetPlacementRefusalFunc
	ceph.ApplyPlacementFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		return fmt.Errorf("%w: run bootstrap-ceph first", ceph.ErrCephNotBootstrapped)
	}
	ceph.StorePlacementPolicyFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		return nil
	}
	ceph.SetPlacementRefusalFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) error {
		return nil
	}
	defer func() {
		ceph.ApplyPlacementFunc = origApply
		ceph.StorePlacementPolicyFunc = origStore
		ceph.SetPlacementRefusalFunc = origRefusal
	}()

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "pre-bootstrap rejection must return 400 not 500")
}

// TestPlacementPutKeepOneReturns400 verifies that the ErrKeepOneInvariant
// sentinel maps to HTTP 400 (not 500).
func TestPlacementPutKeepOneReturns400(t *testing.T) {
	stubPlacementApplyLock(t)
	origApply := ceph.ApplyPlacementFunc
	origStore := ceph.StorePlacementPolicyFunc
	origRefusal := ceph.SetPlacementRefusalFunc
	ceph.ApplyPlacementFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		return fmt.Errorf("%w: refused to remove last mon on node-a", ceph.ErrKeepOneInvariant)
	}
	ceph.StorePlacementPolicyFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		return nil
	}
	ceph.SetPlacementRefusalFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) error {
		return nil
	}
	defer func() {
		ceph.ApplyPlacementFunc = origApply
		ceph.StorePlacementPolicyFunc = origStore
		ceph.SetPlacementRefusalFunc = origRefusal
	}()

	body := `{"mode":"reconcile","members":{"node-a":{"control":false}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "keep-one refusal must return 400 not 500")
}

// TestPlacementPutKeepOneStoresPolicyAndRefusal verifies that a keep-one
// refusal is treated as a partial apply: the requested adds have already taken
// effect, so the policy is persisted as the declared intent (so GET /placement
// can report the observed-vs-declared gap) AND the refusal reason is recorded.
// Both StorePlacementPolicyFunc and SetPlacementRefusalFunc must be called with
// the right arguments, and the response is HTTP 400.
func TestPlacementPutKeepOneStoresPolicyAndRefusal(t *testing.T) {
	stubPlacementApplyLock(t)
	storeCalled := false
	refusalCalled := false
	var storedPolicy types.PlacementPolicy
	var refusalReason string
	origApply := ceph.ApplyPlacementFunc
	origStore := ceph.StorePlacementPolicyFunc
	origRefusal := ceph.SetPlacementRefusalFunc
	ceph.ApplyPlacementFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		return fmt.Errorf("%w: refused to remove last mon on node-a", ceph.ErrKeepOneInvariant)
	}
	ceph.StorePlacementPolicyFunc = func(_ context.Context, _ interfaces.StateInterface, p types.PlacementPolicy) error {
		storeCalled = true
		storedPolicy = p
		return nil
	}
	ceph.SetPlacementRefusalFunc = func(_ context.Context, _ interfaces.StateInterface, reason string) error {
		refusalCalled = true
		refusalReason = reason
		return nil
	}
	defer func() {
		ceph.ApplyPlacementFunc = origApply
		ceph.StorePlacementPolicyFunc = origStore
		ceph.SetPlacementRefusalFunc = origRefusal
	}()

	body := `{"mode":"reconcile","members":{"node-a":{"control":false},"node-b":{"control":true}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "keep-one refusal must return 400 not 500")
	assert.True(t, storeCalled, "StorePlacementPolicy must be called on keep-one refusal (partial apply)")
	assert.True(t, refusalCalled, "SetPlacementRefusal must be called on keep-one refusal")
	assert.Contains(t, refusalReason, "keep-one invariant")
	// The stored policy must equal the requested declared intent.
	ctrlFalse := false
	ctrlTrue := true
	assert.Equal(t, types.PlacementPolicy{
		Mode: types.PlacementModeReconcile,
		Members: map[string]types.MemberPlacement{
			"node-a": {Control: &ctrlFalse},
			"node-b": {Control: &ctrlTrue},
		},
	}, storedPolicy)
}

// TestPlacementPutKeepOneStoreFailureStillRecordsRefusal verifies the
// best-effort path: when StorePlacementPolicyFunc fails on a keep-one refusal,
// the handler still records the refusal reason and returns HTTP 400 rather than
// aborting or returning 500.
func TestPlacementPutKeepOneStoreFailureStillRecordsRefusal(t *testing.T) {
	stubPlacementApplyLock(t)
	storeCalled := false
	refusalCalled := false
	var refusalReason string
	origApply := ceph.ApplyPlacementFunc
	origStore := ceph.StorePlacementPolicyFunc
	origRefusal := ceph.SetPlacementRefusalFunc
	ceph.ApplyPlacementFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		return fmt.Errorf("%w: refused to remove last mon on node-a", ceph.ErrKeepOneInvariant)
	}
	ceph.StorePlacementPolicyFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		storeCalled = true
		return errors.New("database unavailable")
	}
	ceph.SetPlacementRefusalFunc = func(_ context.Context, _ interfaces.StateInterface, reason string) error {
		refusalCalled = true
		refusalReason = reason
		return nil
	}
	defer func() {
		ceph.ApplyPlacementFunc = origApply
		ceph.StorePlacementPolicyFunc = origStore
		ceph.SetPlacementRefusalFunc = origRefusal
	}()

	body := `{"mode":"reconcile","members":{"node-a":{"control":false},"node-b":{"control":true}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.True(t, storeCalled, "StorePlacementPolicy must be attempted on keep-one refusal")
	assert.True(t, refusalCalled, "SetPlacementRefusal must still be called when the policy store fails")
	assert.Contains(t, refusalReason, "keep-one invariant")
	assert.Equal(t, http.StatusBadRequest, rec.Code, "keep-one refusal must return 400 even when the policy store fails")
}

// TestPlacementPutServerErrorReturns500 verifies that a non-client-side
// ApplyPlacement error (e.g. DB failure) does NOT map to 400 but falls through
// to SmartError which returns 500.
func TestPlacementPutServerErrorReturns500(t *testing.T) {
	unlockCalls := stubPlacementApplyLock(t)
	origApply := ceph.ApplyPlacementFunc
	origStore := ceph.StorePlacementPolicyFunc
	origRefusal := ceph.SetPlacementRefusalFunc
	ceph.ApplyPlacementFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		return errors.New("database connection refused")
	}
	ceph.StorePlacementPolicyFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		return nil
	}
	ceph.SetPlacementRefusalFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) error {
		return nil
	}
	defer func() {
		ceph.ApplyPlacementFunc = origApply
		ceph.StorePlacementPolicyFunc = origStore
		ceph.SetPlacementRefusalFunc = origRefusal
	}()

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, "server-side error must return 500")
	assert.Equal(t, 1, *unlockCalls, "the apply lock must be released even when the apply fails")
}

// TestPlacementPutContextDetached verifies that cmdPlacementPut uses a context
// detached from the request's cancellation: even when the request context is
// cancelled, ApplyPlacementFunc receives a non-cancelled context. This prevents
// the "context canceled" error during multi-minute readiness polling.
func TestPlacementPutContextDetached(t *testing.T) {
	stubPlacementApplyLock(t)
	origApply := ceph.ApplyPlacementFunc
	origStore := ceph.StorePlacementPolicyFunc
	origRefusal := ceph.SetPlacementRefusalFunc
	var applyCtxCancelled bool
	ceph.ApplyPlacementFunc = func(ctx context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		select {
		case <-ctx.Done():
			applyCtxCancelled = true
		default:
			applyCtxCancelled = false
		}
		return nil
	}
	ceph.StorePlacementPolicyFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		return nil
	}
	ceph.SetPlacementRefusalFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) error {
		return nil
	}
	defer func() {
		ceph.ApplyPlacementFunc = origApply
		ceph.StorePlacementPolicyFunc = origStore
		ceph.SetPlacementRefusalFunc = origRefusal
	}()

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	// Cancel the request context before the handler runs.
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "handler must succeed despite cancelled request context")
	assert.False(t, applyCtxCancelled, "ApplyPlacementFunc context must be detached from the request's cancellation")
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
// mode is rejected with BadRequest before any lock, apply, or store happens,
// so a future mode (e.g. dry-run) sent to an older snap fails loudly instead
// of being silently applied as a reconcile.
func TestPlacementPutUnknownModeRejected(t *testing.T) {
	unlockCalls := stubPlacementApplyLock(t)
	applyCalled := false
	origApply := ceph.ApplyPlacementFunc
	ceph.ApplyPlacementFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		applyCalled = true
		return nil
	}
	defer func() { ceph.ApplyPlacementFunc = origApply }()

	body := `{"mode":"dry-run","members":{"node-a":{"control":true}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "unknown mode must be rejected with 400")
	assert.False(t, applyCalled, "unknown mode must not be applied")
	assert.Equal(t, 0, *unlockCalls, "unknown mode must be rejected before the lock is taken")
}

// TestPlacementPutReconcileModeAccepted verifies that an explicit "reconcile"
// mode is accepted (200).
func TestPlacementPutReconcileModeAccepted(t *testing.T) {
	stubPlacementApplyLock(t)
	origApply := ceph.ApplyPlacementFunc
	origStore := ceph.StorePlacementPolicyFunc
	origRefusal := ceph.SetPlacementRefusalFunc
	ceph.ApplyPlacementFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		return nil
	}
	ceph.StorePlacementPolicyFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		return nil
	}
	ceph.SetPlacementRefusalFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) error {
		return nil
	}
	defer func() {
		ceph.ApplyPlacementFunc = origApply
		ceph.StorePlacementPolicyFunc = origStore
		ceph.SetPlacementRefusalFunc = origRefusal
	}()

	body := `{"mode":"reconcile","members":{}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "body %s must be accepted", body)
}

// TestPlacementPutMissingModeRejected verifies that an omitted mode is rejected
// with 400 (mode is required, not defaulted to reconcile) before any apply or
// lock interaction.
func TestPlacementPutMissingModeRejected(t *testing.T) {
	unlockCalls := stubPlacementApplyLock(t)
	applyCalled := false
	origApply := ceph.ApplyPlacementFunc
	ceph.ApplyPlacementFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		applyCalled = true
		return nil
	}
	defer func() { ceph.ApplyPlacementFunc = origApply }()

	body := `{"members":{}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "a missing mode must be rejected with 400")
	assert.False(t, applyCalled, "a missing mode must not be applied")
	assert.Equal(t, 0, *unlockCalls, "a missing mode must be rejected before the lock is taken")
}

// TestPlacementPutLockHeldReturnsRetryableError verifies that when another
// placement apply holds the cluster-wide lock, the handler returns an error
// without applying, storing, recording a refusal, or releasing the other
// holder's lock.
func TestPlacementPutLockHeldReturnsRetryableError(t *testing.T) {
	applyCalled := false
	refusalCalled := false
	unlockCalled := false
	origLock := ceph.LockPlacementApplyFunc
	origUnlock := ceph.UnlockPlacementApplyFunc
	origApply := ceph.ApplyPlacementFunc
	origRefusal := ceph.SetPlacementRefusalFunc
	ceph.LockPlacementApplyFunc = func(_ context.Context, _ interfaces.StateInterface) (int64, error) {
		return 0, fmt.Errorf("%w: retry after the current apply completes", ceph.ErrPlacementApplyInProgress)
	}
	ceph.UnlockPlacementApplyFunc = func(_ context.Context, _ interfaces.StateInterface, _ int64) error {
		unlockCalled = true
		return nil
	}
	ceph.ApplyPlacementFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		applyCalled = true
		return nil
	}
	ceph.SetPlacementRefusalFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) error {
		refusalCalled = true
		return nil
	}
	defer func() {
		ceph.LockPlacementApplyFunc = origLock
		ceph.UnlockPlacementApplyFunc = origUnlock
		ceph.ApplyPlacementFunc = origApply
		ceph.SetPlacementRefusalFunc = origRefusal
	}()

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code, "a held apply lock must fail the PUT with 409 Conflict (retryable)")
	assert.False(t, applyCalled, "ApplyPlacement must not run while another apply holds the lock")
	assert.False(t, refusalCalled, "a lock conflict is not a policy refusal and must not overwrite last_refusal")
	assert.False(t, unlockCalled, "the handler must not release a lock it failed to acquire")
}

// TestPlacementPutLockReleasedWithAcquiredToken verifies that the handler
// releases the apply lock with the exact token it acquired, also when the
// apply fails.
func TestPlacementPutLockReleasedWithAcquiredToken(t *testing.T) {
	const token = int64(42)
	var releasedToken int64
	origLock := ceph.LockPlacementApplyFunc
	origUnlock := ceph.UnlockPlacementApplyFunc
	origApply := ceph.ApplyPlacementFunc
	origRefusal := ceph.SetPlacementRefusalFunc
	ceph.LockPlacementApplyFunc = func(_ context.Context, _ interfaces.StateInterface) (int64, error) {
		return token, nil
	}
	ceph.UnlockPlacementApplyFunc = func(_ context.Context, _ interfaces.StateInterface, tok int64) error {
		releasedToken = tok
		return nil
	}
	ceph.ApplyPlacementFunc = func(_ context.Context, _ interfaces.StateInterface, _ types.PlacementPolicy) error {
		return errors.New("apply blew up")
	}
	ceph.SetPlacementRefusalFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) error {
		return nil
	}
	defer func() {
		ceph.LockPlacementApplyFunc = origLock
		ceph.UnlockPlacementApplyFunc = origUnlock
		ceph.ApplyPlacementFunc = origApply
		ceph.SetPlacementRefusalFunc = origRefusal
	}()

	body := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

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
