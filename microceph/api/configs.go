package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/interfaces"
)

// /1.0/configs endpoint.
var configsCmd = rest.Endpoint{
	Path: "configs",

	Get:    rest.EndpointAction{Handler: cmdConfigsGet, ProxyTarget: true},
	Put:    rest.EndpointAction{Handler: cmdConfigsPut, ProxyTarget: true},
	Delete: rest.EndpointAction{Handler: cmdConfigsDelete, ProxyTarget: true},
}

func cmdConfigsGet(s state.State, r *http.Request) response.Response {
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

func cmdConfigsPut(s state.State, r *http.Request) response.Response {
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

	if !req.SkipRestart {
		services := configTable[req.Key].Daemons
		configChangeRefresh(r.Context(), s, services, req.Wait)
	}

	return response.EmptySyncResponse
}

func cmdConfigsDelete(s state.State, r *http.Request) response.Response {
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

	if !req.SkipRestart {
		services := configTable[req.Key].Daemons
		configChangeRefresh(r.Context(), s, services, req.Wait)
	}

	return response.EmptySyncResponse
}

// Perform ordered (one after other) restart of provided Ceph services across the ceph cluster.
func configChangeRefresh(ctx context.Context, s state.State, services []string, wait bool) error {
	if wait {
		// Execute restart synchronously
		err := client.SendRestartRequestToClusterMembers(ctx, s, services)
		if err != nil {
			return err
		}

		// Restart on current host.
		err = ceph.RestartCephServices(ctx, interfaces.CephState{State: s}, services)
		if err != nil {
			return err
		}
	} else { // Execute restart asynchronously
		go func() {
			client.SendRestartRequestToClusterMembers(context.Background(), s, services)
			ceph.RestartCephServices(context.Background(), interfaces.CephState{State: s}, services) // Restart on current host.
		}()
	}

	return nil
}
