package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// /1.0/auth endpoint.
var authCmd = mcTypes.Endpoint{
	Path: "auth",
}

// /1.0/auth/rotate endpoint.
var authRotateCmd = mcTypes.Endpoint{
	Path: "auth/rotate",
	Post: mcTypes.EndpointAction{Handler: cmdAuthRotatePost, ProxyTarget: true},
}

// /1.0/auth/status endpoint.
var authStatusCmd = mcTypes.Endpoint{
	Path: "auth/status",
	Get:  mcTypes.EndpointAction{Handler: cmdAuthStatusGet, ProxyTarget: true},
}

// /1.0/auth/rotate/member endpoint: the per-member daemon rotation step. The
// coordinator calls this on each member (one at a time) so every member rotates
// and restarts only its own daemons, writing keyrings on the machine that runs them.
var authRotateMemberCmd = mcTypes.Endpoint{
	Path: "auth/rotate/member",
	Post: mcTypes.EndpointAction{Handler: cmdAuthRotateMemberPost, ProxyTarget: true},
}

var (
	executeAuthRotationFunc = ceph.ExecuteAuthRotation
	abortAuthRotationFunc   = ceph.AbortAuthRotation
	buildAuthStatusFunc     = ceph.BuildAuthStatus
	rotateMemberDaemonsFunc = ceph.RotateMemberDaemons
)

// cmdAuthRotatePost handles auth rotation initiation or resumption. The
// rotation runs detached on the daemon (so it outlives this request and any
// CLI timeout) and is reported by auth status; this returns the initialized
// record immediately. With abort set, an incomplete rotation record is
// cleared instead; the response then describes the record that was aborted.
func cmdAuthRotatePost(s mcTypes.State, r *http.Request) mcTypes.Response {
	var req types.AuthRotateRequest

	if r.Body != nil && r.ContentLength != 0 {
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil && err.Error() != "EOF" {
			return mcTypes.BadRequest(fmt.Errorf("failed to decode request body: %w", err))
		}
	}

	if req.Abort {
		if req.KeyType != "" || req.Client != "" {
			return mcTypes.BadRequest(fmt.Errorf("abort cannot be combined with key_type or client"))
		}

		record, aborted, err := abortAuthRotationFunc(r.Context(), interfaces.CephState{State: s})
		if err != nil {
			logger.Errorf("Failed aborting auth rotation: %v", err)
			return mcTypes.InternalError(err)
		}
		if !aborted {
			return mcTypes.BadRequest(fmt.Errorf("no incomplete rotation to abort (state: %s)", record.State))
		}

		return mcTypes.SyncResponse(true, types.AuthRotateResponse{
			TargetKeyType: record.TargetKeyType,
			State:         record.State,
			Stage:         record.Stage,
			ClientName:    record.ClientName,
			Blocker:       record.Blocker,
			Detail:        record.Detail,
		})
	}

	record, err := executeAuthRotationFunc(r.Context(), interfaces.CephState{State: s}, req.KeyType, req.Client)
	if err != nil {
		logger.Errorf("Failed executing auth rotation: %v", err)
		return mcTypes.InternalError(err)
	}

	resp := types.AuthRotateResponse{
		TargetKeyType: record.TargetKeyType,
		State:         record.State,
		Stage:         record.Stage,
		ClientName:    record.ClientName,
		Blocker:       record.Blocker,
		Detail:        record.Detail,
	}

	return mcTypes.SyncResponse(true, resp)
}

// cmdAuthStatusGet handles querying auth rotation and client cipher status.
func cmdAuthStatusGet(s mcTypes.State, r *http.Request) mcTypes.Response {
	resp, err := buildAuthStatusFunc(r.Context(), interfaces.CephState{State: s})
	if err != nil {
		logger.Errorf("Failed building auth status: %v", err)
		return mcTypes.InternalError(err)
	}

	return mcTypes.SyncResponse(true, resp)
}

// cmdAuthRotateMemberPost handles the per-member daemon rotation step: this member
// deploys the shared mon. keyring (if it runs a mon), then stops, rotates, and
// restarts only its own mgr/osd/mds daemons, verifying recovery before replying.
func cmdAuthRotateMemberPost(s mcTypes.State, r *http.Request) mcTypes.Response {
	var req types.MemberAuthRotateRequest

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return mcTypes.BadRequest(fmt.Errorf("failed to decode request body: %w", err))
	}

	if req.KeyType == "" {
		return mcTypes.BadRequest(fmt.Errorf("key_type is required for per-member daemon rotation"))
	}

	cephState := interfaces.CephState{State: s}

	// Resume progress: a member whose daemon rotation already completed on a
	// previous attempt replies without rotating anything again.
	if cephState.ClusterState() != nil && slices.Contains(req.Skip, cephState.ClusterState().Name()) {
		return mcTypes.SyncResponse(true, types.MemberAuthRotateResponse{Hostname: cephState.ClusterState().Name()})
	}

	summary, err := rotateMemberDaemonsFunc(r.Context(), cephState, req.KeyType, req.MonKeyring)
	if err != nil {
		logger.Errorf("Failed member daemon rotation: %v", err)
		return mcTypes.InternalError(err)
	}

	osdIDs := make([]string, 0, len(summary.RotatedOSDs))
	for _, id := range summary.RotatedOSDs {
		osdIDs = append(osdIDs, fmt.Sprintf("%d", id))
	}

	resp := types.MemberAuthRotateResponse{
		Hostname:     summary.Hostname,
		MonRestarted: summary.MonRestarted,
		RotatedMgrs:  summary.RotatedMgrs,
		RotatedOSDs:  osdIDs,
		RotatedMDSs:  summary.RotatedMDSs,
	}

	return mcTypes.SyncResponse(true, resp)
}
