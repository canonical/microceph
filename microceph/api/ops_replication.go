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
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"
)

// Top level ops API
var opsCmd = rest.Endpoint{
	Path: "ops",
}

// replication ops API
var opsReplicationCmd = rest.Endpoint{
	Path: "ops/replication/",
}

// List Replications
var opsReplicationWorkloadCmd = rest.Endpoint{
	Path: "ops/replication/{wl}",
	Get:  rest.EndpointAction{Handler: cmdOpsReplication, ProxyTarget: false},
}

// CRUD Replication
var opsReplicationResourceCmd = rest.Endpoint{
	Path:   "ops/replication/{wl}/{name}",
	Get:    rest.EndpointAction{Handler: cmdOpsReplication, ProxyTarget: false},
	Put:    rest.EndpointAction{Handler: cmdOpsReplication, ProxyTarget: false},
	Delete: rest.EndpointAction{Handler: cmdOpsReplication, ProxyTarget: false},
}

// cmdOpsReplication is the common handler for all requests on replication endpoint.
func cmdOpsReplication(s state.State, r *http.Request) response.Response {
	// NOTE (utkarshbhatthere): unescaping API $wl and $name is not required
	// as that information is present in payload.
	var req types.RbdReplicationRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	return handleReplicationRequest(s, r.Context(), req)
}

// handleReplicationRequest parses the replication request and feeds it to the corresponding state machine.
func handleReplicationRequest(s state.State, ctx context.Context, req types.RbdReplicationRequest) response.Response {
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
	err = repFsm.FireCtx(ctx, event, rh, &resp, interfaces.CephState{State: s})
	if err != nil {
		return response.SmartError(err)
	}

	logger.Debugf("REPFSM: Check FSM response: %s", resp)

	// If non-empty response
	if len(resp) > 0 {
		return response.SyncResponse(true, resp)
	}

	return response.SyncResponse(true, "")
}
