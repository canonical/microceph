package api

import (
	"encoding/json"
	"net/http"

	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/state"
	"github.com/lxc/lxd/lxd/response"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/common"
)

// Config table for ConfigExtras
var configTable = ceph.GetConfigTable()

// /1.0/configs endpoint.
var configsCmd = rest.Endpoint{
	Path: "configs",

	Get:  rest.EndpointAction{Handler: cmdConfigsGet, ProxyTarget: true},
	Put: rest.EndpointAction{Handler: cmdConfigsPut, ProxyTarget: true},
	Delete: rest.EndpointAction{Handler: cmdConfigsDelete, ProxyTarget: true},
}

func cmdConfigsGet(s *state.State, r *http.Request) response.Response {
	var req types.Config

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	// Fetch configs.
	configs, err := ceph.ListConfigs(common.CephState{State: s}, req.Key)
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, configs)
}

func cmdConfigsPut(s *state.State, r *http.Request) response.Response {
	var req types.Config

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	if !client.IsForwardedRequest(r) {
		// Call set on the original Request but not on Forwarded Requests.
		err = ceph.SetConfigItem(common.CephState{State: s}, req)
		if err != nil {
			return response.SmartError(err)
		}

		// Forward request to cluster members
		err = client.ForwardConfigRequestToClusterMembers(s, r, &req, client.SetConfig)
	}

	// Restart Daemons on host.
	daemons := configTable[req.Key].Daemons
	for i := range daemons {
		err = ceph.RestartCephService(daemons[i])
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.EmptySyncResponse
}

func cmdConfigsDelete(s *state.State, r *http.Request) response.Response {
	var req types.Config

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	if !client.IsForwardedRequest(r) {
		// Call set on the original Request but not on Forwarded Requests.
		err = ceph.RemoveConfigItem(common.CephState{State: s}, req)
		if err != nil {
			return response.SmartError(err)
		}

		// Forward request to cluster members
		err = client.ForwardConfigRequestToClusterMembers(s, r, &req, client.ClearConfig)
	}

	// Restart Daemons on host.
	daemons := configTable[req.Key].Daemons
	for i := range daemons {
		err = ceph.RestartCephService(daemons[i])
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.EmptySyncResponse
}
