package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// placementCmd is the declarative placement API endpoint (CE142).
var placementCmd = mcTypes.Endpoint{
	Path:   "placement",
	Get:    mcTypes.EndpointAction{Handler: cmdPlacementGet, ProxyTarget: true},
	Put:    mcTypes.EndpointAction{Handler: cmdPlacementPut, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: cmdPlacementDelete, ProxyTarget: true},
}

// cmdPlacementGet returns the current placement status.
func cmdPlacementGet(s mcTypes.State, r *http.Request) mcTypes.Response {
	status, err := ceph.GetPlacementStatusFunc(r.Context(), interfaces.CephState{State: s})
	if err != nil {
		logger.Errorf("failed to get placement status: %v", err)
		return mcTypes.InternalError(err)
	}
	return mcTypes.SyncResponse(true, status)
}

// placementPutTimeout bounds the server-side execution of a placement PUT.
// The placement engine may poll Ceph readiness (MON quorum, MGR standby, MDS
// health) for up to 2 minutes before removing control services, so the
// operation must outlive typical client/proxy timeouts. The CLI retains its
// own shorter client-side timeout; this server-side deadline ensures the
// operation completes and records its result even if the client disconnects.
const placementPutTimeout = 10 * time.Minute

// isClientSidePlacementError reports whether an ApplyPlacement error is a
// client-side precondition failure (not bootstrapped, unknown member,
// keep-one refusal) that should map to HTTP 400 rather than the SmartError 500
// fallback.
func isClientSidePlacementError(err error) bool {
	return errors.Is(err, ceph.ErrCephNotBootstrapped) ||
		errors.Is(err, ceph.ErrUnknownPlacementMember) ||
		errors.Is(err, ceph.ErrKeepOneInvariant)
}

// inProgressResponse maps an "already in progress" sentinel (placement apply or
// Ceph bootstrap) to HTTP 409 Conflict so an orchestrator can distinguish a
// transient, retryable in-progress condition from a genuine server fault (500).
// It returns nil if err is not an in-progress sentinel.
func inProgressResponse(err error) mcTypes.Response {
	if errors.Is(err, ceph.ErrPlacementApplyInProgress) || errors.Is(err, ceph.ErrCephBootstrapInProgress) {
		return mcTypes.ErrorResponse(http.StatusConflict, err.Error())
	}
	return nil
}

// cmdPlacementPut installs and applies a declarative placement policy.
func cmdPlacementPut(s mcTypes.State, r *http.Request) mcTypes.Response {
	var policy types.PlacementPolicy
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&policy)
	if err != nil {
		logger.Errorf("failed decoding placement policy: %v", err)
		return mcTypes.BadRequest(err)
	}

	// Require an explicit mode; only "reconcile" is supported. See
	// types.PlacementModeReconcile. An omitted mode is rejected (not defaulted)
	// so a caller cannot accidentally apply a reconcile with an empty body, and a
	// future mode (e.g. dry-run) sent to an older snap fails loudly.
	if policy.Mode != types.PlacementModeReconcile {
		return mcTypes.BadRequest(fmt.Errorf("placement mode is required; supported mode: %q", types.PlacementModeReconcile))
	}

	// Detach from the request's cancellation while keeping its values (notably
	// the microcluster logger the DB layer reads via log.LoggerFromContext).
	// The placement engine may poll Ceph readiness for up to 2 minutes during
	// keep-one safety checks; without detachment, a client/proxy timeout would
	// cancel the in-flight operation mid-way (e.g. during GetClusterMemberNames
	// which makes a network call to the leader), producing an opaque "context
	// canceled" error and leaving the placement partially applied. This mirrors
	// the BootstrapCeph context detachment pattern.
	ctx, ctxCancel := context.WithTimeout(context.WithoutCancel(r.Context()), placementPutTimeout)
	defer ctxCancel()

	// Serialize placement applies cluster-wide (CE142). ApplyPlacement reads
	// observed service state and then mutates services over minutes; two
	// overlapping PUTs (possibly served by different members) could each count
	// the other's removal targets as keep-one retainers and together remove the
	// last viable control service. The dqlite-backed conditional-UPDATE lock
	// makes the whole read-modify-store cycle mutually exclusive across
	// members; a lease reclaims the lock if a holder crashes mid-apply.
	lockToken, err := ceph.LockPlacementApplyFunc(ctx, interfaces.CephState{State: s})
	if err != nil {
		logger.Errorf("failed to acquire placement apply lock: %v", err)
		// ErrPlacementApplyInProgress is retryable: return HTTP 409 Conflict so
		// an orchestrator can distinguish it from a genuine server fault (500)
		// and key retry logic off the status. Other errors fall through
		// SmartError.
		resp := inProgressResponse(err)
		if resp != nil {
			return resp
		}
		return mcTypes.SmartError(err)
	}
	defer func() {
		// Release with a fresh detached deadline: ctx itself may have expired
		// if the apply consumed the whole placementPutTimeout.
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer releaseCancel()
		unlockErr := ceph.UnlockPlacementApplyFunc(releaseCtx, interfaces.CephState{State: s}, lockToken)
		if unlockErr != nil {
			logger.Warnf("failed to release placement apply lock (a new apply can reclaim it once the lease expires): %v", unlockErr)
		}
	}()

	// Apply (validate + apply) FIRST; only store the policy if apply succeeds.
	// This prevents a rejected policy (e.g. unknown member) from being stored
	// as active.
	applyErr := ceph.ApplyPlacementFunc(ctx, interfaces.CephState{State: s}, policy)
	if applyErr != nil {
		logger.Errorf("failed to apply placement policy: %v", applyErr)

		// A keep-one refusal is a well-defined partial apply: all requested
		// control-service adds have already taken effect in Ceph, and only
		// removals were refused for keep-one safety. Persist the policy as the
		// active declared intent so GET /placement reports the
		// observed-vs-declared gap with last_refusal explaining it, rather than
		// leaving the declared policy stale while the observed services have
		// moved. Other errors do not persist the policy: client-side
		// precondition failures (not bootstrapped, unknown member) fail before
		// any service operation, and a mid-apply server-side failure (e.g. an
		// add that errors partway) also leaves the previously declared policy
		// in place — its partial state is arbitrary rather than a coherent
		// intent, and last_refusal records what failed so the caller can retry
		// the same policy to converge.
		if errors.Is(applyErr, ceph.ErrKeepOneInvariant) {
			storeErr := ceph.StorePlacementPolicyFunc(ctx, interfaces.CephState{State: s}, policy)
			if storeErr != nil {
				logger.Warnf("failed to store placement policy after keep-one refusal: %v", storeErr)
			}
		}

		// Persist the refusal reason so operators/charms polling GET /placement
		// can inspect why the last PUT was rejected. Use the detached context so
		// the refusal is recorded even if the client already disconnected.
		refusalErr := ceph.SetPlacementRefusalFunc(ctx, interfaces.CephState{State: s}, applyErr.Error())
		if refusalErr != nil {
			logger.Warnf("failed to persist placement refusal: %v", refusalErr)
		}
		// Client-side precondition failures (not bootstrapped, unknown member,
		// keep-one) return HTTP 400 so callers can distinguish operator errors
		// from genuine server faults. Other errors (DB failures, etc.) fall
		// through to SmartError which maps known sentinels or returns 500.
		if isClientSidePlacementError(applyErr) {
			return mcTypes.BadRequest(applyErr)
		}
		return mcTypes.SmartError(applyErr)
	}

	// Clear any previous refusal now that the policy applied successfully.
	clearErr := ceph.SetPlacementRefusalFunc(ctx, interfaces.CephState{State: s}, "")
	if clearErr != nil {
		logger.Warnf("failed to clear placement refusal: %v", clearErr)
	}

	// Persist the policy only after successful application.
	err = ceph.StorePlacementPolicyFunc(ctx, interfaces.CephState{State: s}, policy)
	if err != nil {
		logger.Errorf("failed to store placement policy: %v", err)
		return mcTypes.InternalError(err)
	}

	return mcTypes.SyncResponse(true, nil)
}

// cmdPlacementDelete clears the active role-managed placement policy without
// adding or removing services. It acquires the same cluster-wide apply lock as
// cmdPlacementPut so an in-flight PUT cannot re-write the policy after DELETE
// returns 200.
func cmdPlacementDelete(s mcTypes.State, r *http.Request) mcTypes.Response {
	ctx, ctxCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer ctxCancel()

	lockToken, err := ceph.LockPlacementApplyFunc(ctx, interfaces.CephState{State: s})
	if err != nil {
		logger.Errorf("failed to acquire placement apply lock: %v", err)
		// A held lock (in-flight apply) is retryable: return HTTP 409 Conflict
		// so the caller can distinguish it from a server fault.
		resp := inProgressResponse(err)
		if resp != nil {
			return resp
		}
		return mcTypes.SmartError(err)
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer releaseCancel()
		unlockErr := ceph.UnlockPlacementApplyFunc(releaseCtx, interfaces.CephState{State: s}, lockToken)
		if unlockErr != nil {
			logger.Warnf("failed to release placement apply lock (a new apply can reclaim it once the lease expires): %v", unlockErr)
		}
	}()

	err = ceph.ClearPlacementPolicyFunc(ctx, interfaces.CephState{State: s})
	if err != nil {
		logger.Errorf("failed to clear placement policy: %v", err)
		return mcTypes.InternalError(err)
	}
	return mcTypes.SyncResponse(true, nil)
}

// cephBootstrapCmd is the Ceph-only bootstrap API endpoint (CE142).
var cephBootstrapCmd = mcTypes.Endpoint{
	Path: "ceph/bootstrap",
	Put:  mcTypes.EndpointAction{Handler: cmdCephBootstrapPut, ProxyTarget: true},
}

// cmdCephBootstrapPut bootstraps Ceph on an existing MicroCluster member.
func cmdCephBootstrapPut(s mcTypes.State, r *http.Request) mcTypes.Response {
	var req types.CephBootstrapRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&req)
	if err != nil {
		// Only fall back to empty body for EOF (no body); reject malformed JSON.
		if err != io.EOF {
			logger.Errorf("failed decoding ceph bootstrap request: %v", err)
			return mcTypes.BadRequest(err)
		}
		req = types.CephBootstrapRequest{}
	}

	if req.Target == "" {
		req.Target = r.URL.Query().Get("target")
	}
	if req.Target == "" {
		return mcTypes.BadRequest(fmt.Errorf("target member is required"))
	}

	// Defensive guard: the Ceph-only bootstrap handler runs SimpleBootstrapper
	// locally (on whichever daemon receives this request). When ProxyTarget
	// forwarding works correctly, the request reaches the target member and
	// s.Name() == req.Target. If a direct caller sends {"target":"node-b"} to
	// node-a (no proxy), or the proxy routed to the wrong member, the bootstrap
	// would create FSID/config/keyrings on node-a while the lifecycle records
	// node-b — bootstrapping the wrong member. Reject the mismatch.
	localName := s.Name()
	if localName != req.Target {
		logger.Errorf("Ceph-only bootstrap target mismatch: requested %s but running on %s; the request was not proxied to the target member", req.Target, localName)
		return mcTypes.BadRequest(fmt.Errorf("Ceph-only bootstrap target %q does not match local member %q; ensure the request is routed to the target member (e.g. via --target)", req.Target, localName))
	}

	bd := common.BootstrapConfig{
		MonIp:            req.MonIp,
		PublicNet:        req.PublicNet,
		ClusterNet:       req.ClusterNet,
		V2Only:           req.V2Only,
		AvailabilityZone: req.AvailabilityZone,
	}

	err = ceph.BootstrapCephFunc(r.Context(), interfaces.CephState{State: s}, req.Target, bd, req.Force)
	if err != nil {
		logger.Errorf("Ceph-only bootstrap failed: %v", err)
		// Client-side precondition failures (unknown target, partial bootstrap)
		// return HTTP 400 so callers can distinguish operator errors from genuine
		// server faults, mirroring cmdPlacementPut. Other errors fall through to
		// SmartError which maps known sentinels or returns 500.
		if isClientSideBootstrapError(err) {
			return mcTypes.BadRequest(err)
		}
		// Bootstrap already in progress is retryable: return HTTP 409 Conflict
		// so an orchestrator can distinguish it from a genuine server fault.
		resp := inProgressResponse(err)
		if resp != nil {
			return resp
		}
		return mcTypes.SmartError(err)
	}

	return mcTypes.SyncResponse(true, nil)
}

// isClientSideBootstrapError reports whether a BootstrapCeph error is a
// client-side precondition failure (unknown target member, partial bootstrap
// state requiring operator cleanup) that should map to HTTP 400 rather than the
// SmartError 500 fallback. It mirrors isClientSidePlacementError.
func isClientSideBootstrapError(err error) bool {
	return errors.Is(err, ceph.ErrUnknownBootstrapTarget) ||
		errors.Is(err, ceph.ErrPartialBootstrap)
}
