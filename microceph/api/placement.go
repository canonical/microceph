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

// cmdPlacementPut installs and applies a declarative placement policy.
func cmdPlacementPut(s mcTypes.State, r *http.Request) mcTypes.Response {
	var policy types.PlacementPolicy
	err := json.NewDecoder(r.Body).Decode(&policy)
	if err != nil {
		logger.Errorf("failed decoding placement policy: %v", err)
		return mcTypes.BadRequest(err)
	}

	// Detach from the request's cancellation while keeping its values (notably
	// the microcluster logger the DB layer reads via log.LoggerFromContext).
	// The placement engine may poll Ceph readiness for up to 2 minutes during
	// keep-one safety checks; without detachment, a client/proxy timeout would
	// cancel the in-flight operation mid-way (e.g. during GetClusterMemberNames
	// which makes a network call to the leader), producing an opaque "context
	// canceled" error and leaving the placement partially applied. This mirrors
	// the CephOnlyBootstrap context detachment pattern.
	ctx, ctxCancel := context.WithTimeout(context.WithoutCancel(r.Context()), placementPutTimeout)
	defer ctxCancel()

	// Apply (validate + apply) FIRST; only store the policy if apply succeeds.
	// This prevents a rejected policy (e.g. unknown member) from being stored
	// as active.
	applyErr := ceph.ApplyPlacementFunc(ctx, interfaces.CephState{State: s}, policy)
	if applyErr != nil {
		logger.Errorf("failed to apply placement policy: %v", applyErr)
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
// adding or removing services.
func cmdPlacementDelete(s mcTypes.State, r *http.Request) mcTypes.Response {
	err := ceph.ClearPlacementPolicyFunc(r.Context(), interfaces.CephState{State: s})
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
	err := json.NewDecoder(r.Body).Decode(&req)
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

	err = ceph.CephOnlyBootstrapFunc(r.Context(), interfaces.CephState{State: s}, req.Target, bd, req.Force)
	if err != nil {
		logger.Errorf("Ceph-only bootstrap failed: %v", err)
		return mcTypes.SyncResponse(false, err)
	}

	return mcTypes.SyncResponse(true, nil)
}
