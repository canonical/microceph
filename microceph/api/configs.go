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
)

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
	configs, err := ceph.ListConfigs()
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, configs)
}

func cmdConfigsPut(s *state.State, r *http.Request) response.Response {
	var req types.Config
	configTable := ceph.GetConfigTable()

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	// Configure the key/value
	err = ceph.SetConfigItem(req)
	if err != nil {
		return response.SmartError(err)
	}

	services := configTable[req.Key].Daemons
	go func() {
		client.SendRestartRequestToClusterMembers(s, r, services)
		// Restart Daemons on host.
		ceph.RestartCephServices(services)
	}()

	return response.EmptySyncResponse
}

func cmdConfigsDelete(s *state.State, r *http.Request) response.Response {
	var req types.Config
	configTable := ceph.GetConfigTable()

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	// Clean the key/value
	err = ceph.RemoveConfigItem(req)
	if err != nil {
		return response.SmartError(err)
	}

	services := configTable[req.Key].Daemons
	go func() {
		client.SendRestartRequestToClusterMembers(s, r, services)
		// Restart Daemons on host.
		ceph.RestartCephServices(services)
	}()

	return response.EmptySyncResponse
}
