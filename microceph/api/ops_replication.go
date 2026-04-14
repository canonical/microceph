package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/gorilla/mux"
)

// Top level ops API
var opsCmd = mcTypes.Endpoint{
	Path: "ops",
}

// replication ops API
var opsReplicationCmd = mcTypes.Endpoint{
	Path: "ops/replication/",
}

// List Replications
var opsReplicationWorkloadCmd = mcTypes.Endpoint{
	Path: "ops/replication/{wl}",
	Get:  mcTypes.EndpointAction{Handler: getOpsReplicationWorkload, ProxyTarget: false},
	Put:  mcTypes.EndpointAction{Handler: putOpsReplicationWorkload, ProxyTarget: false},
}

// CRUD Replication
var opsReplicationResourceCmd = mcTypes.Endpoint{
	Path:   "ops/replication/{wl}/{name}",
	Get:    mcTypes.EndpointAction{Handler: getOpsReplicationResource, ProxyTarget: false},
	Post:   mcTypes.EndpointAction{Handler: postOpsReplicationResource, ProxyTarget: false},
	Put:    mcTypes.EndpointAction{Handler: putOpsReplicationResource, ProxyTarget: false},
	Delete: mcTypes.EndpointAction{Handler: deleteOpsReplicationResource, ProxyTarget: false},
}

// getOpsReplicationWorkload handles list operation
func getOpsReplicationWorkload(s mcTypes.State, r *http.Request) mcTypes.Response {
	return cmdOpsReplication(s, r, types.ListReplicationRequest)
}

// putOpsReplicationWorkload handles site level (promote/demote) operation
func putOpsReplicationWorkload(s mcTypes.State, r *http.Request) mcTypes.Response {
	// either promote or demote (already encoded in request)
	return cmdOpsReplication(s, r, types.WorkloadReplicationRequest)
}

// getOpsReplicationResource handles status operation for a certain resource.
func getOpsReplicationResource(s mcTypes.State, r *http.Request) mcTypes.Response {
	return cmdOpsReplication(s, r, types.StatusReplicationRequest)
}

// postOpsReplicationResource handles rep enablement for the requested resource
func postOpsReplicationResource(s mcTypes.State, r *http.Request) mcTypes.Response {
	return cmdOpsReplication(s, r, types.EnableReplicationRequest)
}

// putOpsReplicationResource handles configuration of the requested resource
func putOpsReplicationResource(s mcTypes.State, r *http.Request) mcTypes.Response {
	return cmdOpsReplication(s, r, types.ConfigureReplicationRequest)
}

// deleteOpsReplicationResource handles rep disablement for the requested resource
func deleteOpsReplicationResource(s mcTypes.State, r *http.Request) mcTypes.Response {
	return cmdOpsReplication(s, r, types.DisableReplicationRequest)
}

// cmdOpsReplication is the common handler for all requests on replication endpoint.
func cmdOpsReplication(s mcTypes.State, r *http.Request, overwriteType types.ReplicationRequestType) mcTypes.Response {
	// Get workload name from API
	wl, err := url.PathUnescape(mux.Vars(r)["wl"])
	if err != nil {
		logger.Errorf("REPOPS: %v", err.Error())
		return mcTypes.InternalError(err)
	}

	// Get resource name from API
	resource, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		logger.Errorf("REPOPS: %v", err.Error())
		return mcTypes.InternalError(err)
	}

	// Populate the replication request with necessary information for RESTfullnes
	var req types.ReplicationRequest
	switch wl {
	case string(types.RbdWorkload):
		var data types.RbdReplicationRequest
		err := json.NewDecoder(r.Body).Decode(&data)
		if err != nil {
			logger.Errorf("REPOPS: failed to decode request data: %v", err.Error())
			return mcTypes.InternalError(err)
		}

		// carry RbdReplicationRequest in interface object.
		err = data.SetAPIObjectID(resource)
		if err != nil {
			return mcTypes.InternalError(err)
		}
		// If the request is not WorkloadReplicationRequest, set the request type.
		data.OverwriteRequestType(overwriteType)
		req = data
	case string(types.CephFsWorkload):
		var data types.CephfsReplicationRequest
		err := json.NewDecoder(r.Body).Decode(&data)
		if err != nil {
			logger.Errorf("REPOPS: failed to decode request data: %v", err.Error())
			return mcTypes.InternalError(err)
		}
		// If the request is not WorkloadReplicationRequest, set the request type.
		data.OverwriteRequestType(overwriteType)
		req = data
	default:
		return mcTypes.SmartError(fmt.Errorf("unknown workload %s, resource %s", wl, resource))
	}

	logger.Debugf("REPOPS: %s received for %s: %s", req.GetWorkloadRequestType(), wl, resource)
	return handleReplicationRequest(s, r.Context(), req)
}

// handleReplicationRequest parses the replication request and feeds it to the corresponding state machine.
func handleReplicationRequest(s mcTypes.State, ctx context.Context, req types.ReplicationRequest) mcTypes.Response {
	// Fetch replication handler
	wl := string(req.GetWorkloadType())
	rh := ceph.GetReplicationHandler(wl)
	if rh == nil {
		return mcTypes.SmartError(fmt.Errorf("no replication handler for %s workload", wl))
	}

	// Populate resource info
	err := rh.PreFill(ctx, req)
	if err != nil {
		return mcTypes.SmartError(err)
	}

	// Get FSM
	state, err := rh.GetResourceState()
	if err != nil {
		return mcTypes.SmartError(err)
	}
	repFsm := ceph.GetReplicationStateMachine(state)

	var resp string
	event := req.GetWorkloadRequestType()
	// Each event is provided with, replication handler, response object and state.
	err = repFsm.FireCtx(ctx, event, rh, &resp, interfaces.CephState{State: s})
	if err != nil {
		return mcTypes.SmartError(err)
	}

	logger.Debugf("REPFSM: Check FSM response: %s", resp)

	// If non-empty response
	if len(resp) > 0 {
		return mcTypes.SyncResponse(true, resp)
	}

	return mcTypes.SyncResponse(true, "")
}
