package api

import (
	"encoding/json"
	"github.com/canonical/lxd/shared/logger"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/state"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
)

// /1.0/pool endpoint.
var poolsCmd = rest.Endpoint{
	Path: "pools-op",
	Put: rest.EndpointAction{Handler: cmdPoolsPut, ProxyTarget: true},
}

func cmdPoolsPut(s *state.State, r *http.Request) response.Response {
	var req types.PoolPut

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	logger.Debugf("cmdPoolPut: %v", req)
	err = ceph.SetReplicationFactor(req.Pools, req.Size)
	if err != nil {
		return response.SmartError(err)
	}

	logger.Debugf("cmdPoolPut done: %v", req)
	return response.EmptySyncResponse
}
