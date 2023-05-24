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

	Get:    rest.EndpointAction{Handler: cmdConfigsGet, ProxyTarget: true},
	Put:    rest.EndpointAction{Handler: cmdConfigsPut, ProxyTarget: true},
	Delete: rest.EndpointAction{Handler: cmdConfigsDelete, ProxyTarget: true},
}

func cmdConfigsGet(s *state.State, r *http.Request) response.Response {
	var err error
	var req types.Config
	var configs types.Configs

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	// If a valid key string is passed, fetch that key.
	if len(req.Key) > 0 {
		configs, err = ceph.GetConfigItem(req)
	} else {
		// Fetch all configs.
		configs, err = ceph.ListConfigs()
	}
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, configs)
}

func cmdConfigsPut(s *state.State, r *http.Request) response.Response {
	var req types.Config
	configTable := ceph.GetConstConfigTable()

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
	configChangeRefresh(s, services, req.Wait)

	return response.EmptySyncResponse
}

func cmdConfigsDelete(s *state.State, r *http.Request) response.Response {
	var req types.Config
	configTable := ceph.GetConstConfigTable()

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
	configChangeRefresh(s, services, req.Wait)

	return response.EmptySyncResponse
}

// Perform ordered (one after other) restart of provided Ceph services across the ceph cluster.
func configChangeRefresh(s *state.State, services []string, wait bool) error {
	if wait {
		// Execute restart synchronously
		err := client.SendRestartRequestToClusterMembers(s, services)
		if err != nil {
			return err
		}

		// Restart on current host.
		err = ceph.RestartCephServices(services)
		if err != nil {
			return err
		}
	} else { // Execute restart asynchronously
		go func() {
			client.SendRestartRequestToClusterMembers(s, services)
			ceph.RestartCephServices(services) // Restart on current host.
		}()
	}

	return nil
}
