package api

import (
	"encoding/json"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"
)

// top level microceph API
var microcephCmd = rest.Endpoint{
	Path: "microceph",
}

// microceph configs API
var microcephConfigsCmd = rest.Endpoint{
	Path: "microceph/configs",
}

var logLevelCmd = rest.Endpoint{
	Path: "microceph/configs/log-level",
	Put:  rest.EndpointAction{Handler: logLevelPut, ProxyTarget: true},
	Get:  rest.EndpointAction{Handler: logLevelGet, ProxyTarget: true},
}

func logLevelPut(s state.State, r *http.Request) response.Response {
	var req types.LogLevelPut

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	logger.Debugf("cmdLogLevelPut: %v", req)
	err = ceph.SetLogLevel(req.Level)
	if err != nil {
		return response.SmartError(err)
	}

	logger.Debugf("cmdLogLevelPut done: %v", req)
	return response.EmptySyncResponse
}

func logLevelGet(s state.State, r *http.Request) response.Response {
	return response.SyncResponse(true, ceph.GetLogLevel())
}
