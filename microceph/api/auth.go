package api

import (
	"encoding/json"
	"fmt"
	"net/http"

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

var (
	executeAuthRotationFunc = ceph.ExecuteAuthRotation
	buildAuthStatusFunc     = ceph.BuildAuthStatus
)

// cmdAuthRotatePost handles auth rotation initiation or resumption.
func cmdAuthRotatePost(s mcTypes.State, r *http.Request) mcTypes.Response {
	var req types.AuthRotateRequest

	if r.Body != nil && r.ContentLength != 0 {
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil && err.Error() != "EOF" {
			return mcTypes.BadRequest(fmt.Errorf("failed to decode request body: %w", err))
		}
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
