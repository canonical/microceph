package api

import (
	"encoding/json"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"net/http"

	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/state"
	"github.com/lxc/lxd/lxd/response"

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
