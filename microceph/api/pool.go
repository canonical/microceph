package api

import (
	"encoding/json"
	"net/http"

	"github.com/canonical/microceph/microceph/logger"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
)

// /1.0/pools-op endpoint.
var poolsOpCmd = rest.Endpoint{
	Path: "pools-op",
	Put:  rest.EndpointAction{Handler: cmdPoolsPut, ProxyTarget: true},
}

// /1.0/pools endpoint.
var poolsCmd = rest.Endpoint{
	Path: "pools",
	Get:  rest.EndpointAction{Handler: cmdPoolsGet, ProxyTarget: true},
}

func cmdPoolsGet(s state.State, r *http.Request) response.Response {
	logger.Debug("cmdPoolGet")
	pools, err := ceph.GetOSDPools()
	if err != nil {
		return response.SmartError(err)
	}

	logger.Debug("cmdPoolGet done")

	return response.SyncResponse(true, pools)
}

func cmdPoolsPut(s state.State, r *http.Request) response.Response {
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
