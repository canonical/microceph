package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"
)

// Top level client API
var opsCmd = rest.Endpoint{
	Path: "ops",
}

// client configs API
var opsReplicationCmd = rest.Endpoint{
	Path: "ops/replication",
}

var opsReplicationRbdCmd = rest.Endpoint{
	Path:   "ops/replication/rbd/{name}",
	Get:    rest.EndpointAction{Handler: cmdOpsReplicationRbdGet, ProxyTarget: false},
	Put:    rest.EndpointAction{Handler: cmdOpsReplicationRbdPut, ProxyTarget: false},
	Delete: rest.EndpointAction{Handler: cmdOpsReplicationRbdDelete, ProxyTarget: false},
}

// cmdOpsReplicationGet fetches all configured replication pairs.
func cmdOpsReplicationRbdGet(s state.State, r *http.Request) response.Response {
	var req types.RbdReplicationRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	return handleReplicationRequest(s, r.Context(), req)
}

// cmdOpsReplicationRbdPut configures a new RBD replication pair.
func cmdOpsReplicationRbdPut(s state.State, r *http.Request) response.Response {
	var req types.RbdReplicationRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	return handleReplicationRequest(s, r.Context(), req)
}

// cmdOpsReplicationRbdDelete deletes a configured replication pair.
func cmdOpsReplicationRbdDelete(s state.State, r *http.Request) response.Response {
	var req types.RbdReplicationRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	return handleReplicationRequest(s, r.Context(), req)
}

func handleReplicationRequest(s *state.State, ctx context.Context, req types.RbdReplicationRequest) response.Response {
	// Fetch replication handler
	wl := string(req.GetWorkloadType())
	rh := ceph.GetReplicationHandler(wl)
	if rh == nil {
		return response.SmartError(fmt.Errorf("no replication handler for %s workload", wl))
	}

	// Populate resource info
	err := rh.PreFill(ctx, req)
	if err != nil {
		return response.SmartError(err)
	}

	// Get FSM
	repFsm := ceph.GetReplicationStateMachine(rh.GetResourceState())

	var resp string
	event := req.GetWorkloadRequestType()
	// Each event is provided with, replication handler, response object and state.
	err = repFsm.FireCtx(ctx, event, rh, &resp, s)
	if err != nil {
		return response.SmartError(err)
	}

	logger.Infof("Bazinga: Check FSM response: %s", resp)

	// If non-empty response
	if len(resp) > 0 {
		return response.SyncResponse(true, resp)
	}

	return response.SyncResponse(true, "")
}
