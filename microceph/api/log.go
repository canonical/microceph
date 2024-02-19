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

// /1.0/log-level endpoint.
var logCmd = rest.Endpoint{
	Path: "log-level",
	Put:  rest.EndpointAction{Handler: logLevelPut, ProxyTarget: true},
	Get:  rest.EndpointAction{Handler: logLevelGet, ProxyTarget: true},
}

func logLevelPut(s *state.State, r *http.Request) response.Response {
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

func logLevelGet(s *state.State, r *http.Request) response.Response {
	return response.SyncResponse(true, ceph.GetLogLevel())
}
