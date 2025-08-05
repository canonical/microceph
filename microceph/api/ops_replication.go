package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/microceph/microceph/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"
	"github.com/gorilla/mux"
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
	Get:  rest.EndpointAction{Handler: getOpsReplicationWorkload, ProxyTarget: false},
	Put:  rest.EndpointAction{Handler: putOpsReplicationWorkload, ProxyTarget: false},
}

// CRUD Replication
var opsReplicationResourceCmd = rest.Endpoint{
	Path:   "ops/replication/{wl}/{name}",
	Get:    rest.EndpointAction{Handler: getOpsReplicationResource, ProxyTarget: false},
	Post:   rest.EndpointAction{Handler: postOpsReplicationResource, ProxyTarget: false},
	Put:    rest.EndpointAction{Handler: putOpsReplicationResource, ProxyTarget: false},
	Delete: rest.EndpointAction{Handler: deleteOpsReplicationResource, ProxyTarget: false},
}

// getOpsReplicationWorkload handles list operation
func getOpsReplicationWorkload(s state.State, r *http.Request) response.Response {
	return cmdOpsReplication(s, r, types.ListReplicationRequest)
}

// putOpsReplicationWorkload handles site level (promote/demote) operation
func putOpsReplicationWorkload(s state.State, r *http.Request) response.Response {
	// either promote or demote (already encoded in request)
	return cmdOpsReplication(s, r, types.WorkloadReplicationRequest)
}

// getOpsReplicationResource handles status operation for a certain resource.
func getOpsReplicationResource(s state.State, r *http.Request) response.Response {
	return cmdOpsReplication(s, r, types.StatusReplicationRequest)
}

// postOpsReplicationResource handles rep enablement for the requested resource
func postOpsReplicationResource(s state.State, r *http.Request) response.Response {
	return cmdOpsReplication(s, r, types.EnableReplicationRequest)
}

// putOpsReplicationResource handles configuration of the requested resource
func putOpsReplicationResource(s state.State, r *http.Request) response.Response {
	return cmdOpsReplication(s, r, types.ConfigureReplicationRequest)
}

// deleteOpsReplicationResource handles rep disablement for the requested resource
func deleteOpsReplicationResource(s state.State, r *http.Request) response.Response {
	return cmdOpsReplication(s, r, types.DisableReplicationRequest)
}

// cmdOpsReplication is the common handler for all requests on replication endpoint.
func cmdOpsReplication(s state.State, r *http.Request, patchRequest types.ReplicationRequestType) response.Response {
	// Get workload name from API
	wl, err := url.PathUnescape(mux.Vars(r)["wl"])
	if err != nil {
		logger.Errorf("REP: %v", err.Error())
		return response.InternalError(err)
	}

	// Get resource name from API
	resource, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		logger.Errorf("REP: %v", err.Error())
		return response.InternalError(err)
	}

	// Populate the replication request with necessary information for RESTfullnes
	var req types.ReplicationRequest
	if wl == string(types.RbdWorkload) {
		var data types.RbdReplicationRequest
		err := json.NewDecoder(r.Body).Decode(&data)
		if err != nil {
			logger.Errorf("REP: failed to decode request data: %v", err.Error())
			return response.InternalError(err)
		}

		// carry RbdReplicationRequest in interface object.
		data.SetAPIObjectId(resource)
		// If the request is not WorkloadReplicationRequest, set the request type.
		if len(patchRequest) != 0 {
			data.RequestType = patchRequest
		}

		req = data
	} else {
		return response.SmartError(fmt.Errorf("unknown workload %s, resource %s", wl, resource))
	}

	logger.Debugf("REPOPS: %s received for %s: %s", req.GetWorkloadRequestType(), wl, resource)

	return handleReplicationRequest(s, r.Context(), req)
}

// handleReplicationRequest parses the replication request and feeds it to the corresponding state machine.
func handleReplicationRequest(s state.State, ctx context.Context, req types.ReplicationRequest) response.Response {
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
