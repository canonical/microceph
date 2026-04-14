package api

import (
	"encoding/json"
	"net/http"

	"github.com/canonical/microceph/microceph/logger"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
)

// /1.0/pools-op endpoint.
var poolsOpCmd = mcTypes.Endpoint{
	Path: "pools-op",
	Put:  mcTypes.EndpointAction{Handler: cmdPoolsPut, ProxyTarget: true},
}

// /1.0/pools endpoint.
var poolsCmd = mcTypes.Endpoint{
	Path: "pools",
	Get:  mcTypes.EndpointAction{Handler: cmdPoolsGet, ProxyTarget: true},
}

func cmdPoolsGet(s mcTypes.State, r *http.Request) mcTypes.Response {
	logger.Debug("cmdPoolGet")
	pools, err := ceph.GetOSDPools(r.Context())
	if err != nil {
		return mcTypes.SmartError(err)
	}

	logger.Debug("cmdPoolGet done")

	return mcTypes.SyncResponse(true, pools)
}

func cmdPoolsPut(s mcTypes.State, r *http.Request) mcTypes.Response {
	var req types.PoolPut

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	logger.Debugf("cmdPoolPut: %v", req)
	err = ceph.SetReplicationFactor(req.Pools, req.Size)
	if err != nil {
		return mcTypes.SmartError(err)
	}

	logger.Debugf("cmdPoolPut done: %v", req)
	return mcTypes.EmptySyncResponse
}
