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

// TestPlacementPutSuccess verifies that cmdPlacementPut decodes the policy,
// applies it, stores it, and returns success.
func TestPlacementPutSuccess(t *testing.T) {
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

	body := `{"members":{"node-a":{"control":true}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, applyCalled)
	assert.True(t, storeCalled)
}

// TestPlacementPutApplyFailureNotStored (N3) verifies that when ApplyPlacement
// fails (e.g. unknown member), StorePlacementPolicy is NOT called and the
// response is HTTP 400 (BadRequest) because the error is a client-side sentinel.
func TestPlacementPutApplyFailureNotStored(t *testing.T) {
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

	body := `{"members":{"bad-node":{"control":true}}}`
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

	body := `{"members":{"node-a":{"control":true}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "pre-bootstrap rejection must return 400 not 500")
}

// TestPlacementPutKeepOneReturns400 verifies that the ErrKeepOneInvariant
// sentinel maps to HTTP 400 (not 500).
func TestPlacementPutKeepOneReturns400(t *testing.T) {
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

	body := `{"members":{"node-a":{"control":false}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "keep-one refusal must return 400 not 500")
}

// TestPlacementPutServerErrorReturns500 verifies that a non-client-side
// ApplyPlacement error (e.g. DB failure) does NOT map to 400 but falls through
// to SmartError which returns 500.
func TestPlacementPutServerErrorReturns500(t *testing.T) {
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

	body := `{"members":{"node-a":{"control":true}}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/placement", strings.NewReader(body))

	resp := cmdPlacementPut(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, "server-side error must return 500")
}

// TestPlacementPutContextDetached verifies that cmdPlacementPut uses a context
// detached from the request's cancellation: even when the request context is
// cancelled, ApplyPlacementFunc receives a non-cancelled context. This prevents
// the "context canceled" error during multi-minute readiness polling.
func TestPlacementPutContextDetached(t *testing.T) {
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

	body := `{"members":{"node-a":{"control":true}}}`
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

// TestPlacementDeleteSuccess verifies that cmdPlacementDelete calls
// ClearPlacementPolicy and returns success.
func TestPlacementDeleteSuccess(t *testing.T) {
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
}

// TestPlacementGetSuccess verifies that cmdPlacementGet returns placement status.
func TestPlacementGetSuccess(t *testing.T) {
	origGet := ceph.GetPlacementStatusFunc
	ceph.GetPlacementStatusFunc = func(_ context.Context, _ interfaces.StateInterface) (*types.PlacementStatus, error) {
		return &types.PlacementStatus{
			Active:           true,
			BootstrapState:   "bootstrapped",
			CephBootstrapped: true,
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
	assert.True(t, raw.Metadata.CephBootstrapped)
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
	origBootstrap := ceph.CephOnlyBootstrapFunc
	ceph.CephOnlyBootstrapFunc = func(_ context.Context, _ interfaces.StateInterface, target string, bd common.BootstrapConfig, _ bool) error {
		capturedTarget = target
		capturedBd = bd
		return nil
	}
	defer func() { ceph.CephOnlyBootstrapFunc = origBootstrap }()

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
	origBootstrap := ceph.CephOnlyBootstrapFunc
	ceph.CephOnlyBootstrapFunc = func(_ context.Context, _ interfaces.StateInterface, target string, _ common.BootstrapConfig, _ bool) error {
		capturedTarget = target
		return nil
	}
	defer func() { ceph.CephOnlyBootstrapFunc = origBootstrap }()

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
	origBootstrap := ceph.CephOnlyBootstrapFunc
	ceph.CephOnlyBootstrapFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig, _ bool) error {
		bootstrapCalled = true
		return nil
	}
	defer func() { ceph.CephOnlyBootstrapFunc = origBootstrap }()

	body := `{"target":"node-b"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/ceph/bootstrap", strings.NewReader(body))

	resp := cmdCephBootstrapPut(newTestState("node-a"), req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "mismatched target must be rejected")
	assert.False(t, bootstrapCalled, "CephOnlyBootstrap must not run on the wrong member")
}

// TestCephBootstrapPutInProgress verifies that ErrCephBootstrapInProgress maps
// to a SyncResponse(false).
func TestCephBootstrapPutInProgress(t *testing.T) {
	origBootstrap := ceph.CephOnlyBootstrapFunc
	ceph.CephOnlyBootstrapFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig, _ bool) error {
		return ceph.ErrCephBootstrapInProgress
	}
	defer func() { ceph.CephOnlyBootstrapFunc = origBootstrap }()

	body := `{"target":"node-b"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/ceph/bootstrap", strings.NewReader(body))

	resp := cmdCephBootstrapPut(newTestState("node-b"), req)
	_ = resp.Render(rec, req)

	// SyncResponse(false, ...) returns 200 with error in body.
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestCephBootstrapPutAlreadyBootstrapped verifies that already-bootstrapped
// (nil from CephOnlyBootstrap) maps to success.
func TestCephBootstrapPutAlreadyBootstrapped(t *testing.T) {
	origBootstrap := ceph.CephOnlyBootstrapFunc
	ceph.CephOnlyBootstrapFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig, _ bool) error {
		return nil // no-op success
	}
	defer func() { ceph.CephOnlyBootstrapFunc = origBootstrap }()

	body := `{"target":"node-b"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/ceph/bootstrap", strings.NewReader(body))

	resp := cmdCephBootstrapPut(newTestState("node-b"), req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestCephBootstrapPutUnknownMember verifies that unknown-member errors map to
// SyncResponse(false).
func TestCephBootstrapPutUnknownMember(t *testing.T) {
	origBootstrap := ceph.CephOnlyBootstrapFunc
	ceph.CephOnlyBootstrapFunc = func(_ context.Context, _ interfaces.StateInterface, _ string, _ common.BootstrapConfig, _ bool) error {
		return ceph.ErrUnknownBootstrapTarget
	}
	defer func() { ceph.CephOnlyBootstrapFunc = origBootstrap }()

	body := `{"target":"node-b"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/ceph/bootstrap", strings.NewReader(body))

	resp := cmdCephBootstrapPut(newTestState("node-b"), req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
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
