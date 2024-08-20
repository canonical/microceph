package api

import (
	"context"
	"encoding/json"
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

	return handleRbdRepRequest(r.Context(), req)
}

// cmdOpsReplicationRbdPut configures a new RBD replication pair.
func cmdOpsReplicationRbdPut(s state.State, r *http.Request) response.Response {
	var req types.RbdReplicationRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	return handleRbdRepRequest(r.Context(), req)
}

// cmdOpsReplicationRbdDelete deletes a configured replication pair.
func cmdOpsReplicationRbdDelete(s state.State, r *http.Request) response.Response {
	var req types.RbdReplicationRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	return handleRbdRepRequest(r.Context(), req)
}

func handleRbdRepRequest(ctx context.Context, req types.RbdReplicationRequest) response.Response {
	repFsm := ceph.CreateReplicationFSM(ceph.GetRbdMirroringState(req.GetAPIObjectId()), req)
	logger.Infof("Bazinga: Check available transitions: %v", repFsm.AvailableTransitions())
	err := repFsm.Event(ctx, req.GetWorkloadRequestType())
	if err != nil {
		return response.SmartError(err)
	}

	resp, ok := repFsm.Metadata("response")
	if ok {
		return response.SyncResponse(true, resp)
	}

	return response.EmptySyncResponse
}
