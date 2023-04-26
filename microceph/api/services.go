package api

import (
	"encoding/json"
	"net/http"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"

	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/state"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared/logger"

	"github.com/canonical/microceph/microceph/ceph"
)

// /1.0/services endpoint.
var servicesCmd = rest.Endpoint{
	Path: "services",

	Get: rest.EndpointAction{Handler: cmdServicesGet, ProxyTarget: true},
}

func cmdServicesGet(s *state.State, r *http.Request) response.Response {
	services, err := ceph.ListServices(s)
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, services)
}

// Service Reload Endpoint.
var restartServiceCmd = rest.Endpoint{
	Path: "services/restart",
	Post: rest.EndpointAction{Handler: cmdRestartServicePost, ProxyTarget: true},
}

func cmdRestartServicePost(s *state.State, r *http.Request) response.Response {
	var services types.Services

	err := json.NewDecoder(r.Body).Decode(&services)
	if err != nil {
		logger.Errorf("Failed decoding restart services: %v", err)
		return response.InternalError(err)
	}

	for _, service := range services {
		err = ceph.RestartCephService(service.Service)
		if err != nil {
			url := s.Address().String()
			logger.Errorf("Failed restarting %s on host %s", service.Service, url)
			return response.SyncResponse(false, err)
		}
	}

	return response.EmptySyncResponse
}

var rgwServiceCmd = rest.Endpoint{
	Path: "services/rgw",

	Put: rest.EndpointAction{Handler: cmdRGWServicePut, ProxyTarget: true},
}

// cmdRGWServicePutRGW is the handler for PUT /1.0/services/rgw.
func cmdRGWServicePut(s *state.State, r *http.Request) response.Response {
	var req types.RGWService

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	if req.Enabled {
		err = ceph.EnableRGW(common.CephState{State: s}, req.Port)
	} else {
		err = ceph.DisableRGW(common.CephState{State: s})
	}
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
