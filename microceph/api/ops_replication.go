package api

import (
	"encoding/json"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/state"
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
func cmdOpsReplicationRbdGet(s *state.State, r *http.Request) response.Response {
	var req types.RbdReplicationRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	logger.Errorf("BAZINGA %v", req) // TODO: Remove

	// TODO: Implement
	return response.EmptySyncResponse
}

// cmdOpsReplicationRbdPut configures a new RBD replication pair.
func cmdOpsReplicationRbdPut(s *state.State, r *http.Request) response.Response {
	var req types.RbdReplicationRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	logger.Errorf("BAZINGA %v", req) // TODO: Remove

	err = ceph.EnableRbdReplication(req)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// cmdOpsReplicationRbdDelete deletes a configured replication pair.
func cmdOpsReplicationRbdDelete(s *state.State, r *http.Request) response.Response {
	var req types.RbdReplicationRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	logger.Errorf("BAZINGA %v", req) // TODO: Remove

	// TODO: Implement
	return response.EmptySyncResponse
}
