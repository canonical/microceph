package api

import (
	"context"
	"encoding/json"
	"net/http"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// /1.0/configs endpoint.
var configsCmd = mcTypes.Endpoint{
	Path: "configs",

	Get:    mcTypes.EndpointAction{Handler: cmdConfigsGet, ProxyTarget: true},
	Put:    mcTypes.EndpointAction{Handler: cmdConfigsPut, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: cmdConfigsDelete, ProxyTarget: true},
}

func cmdConfigsGet(s mcTypes.State, r *http.Request) mcTypes.Response {
	var err error
	var req types.Config
	var configs types.Configs

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	// If a valid key string is passed, fetch that key.
	if len(req.Key) > 0 {
		configs, err = ceph.GetConfigItem(req)
	} else {
		// Fetch all configs.
		configs, err = ceph.ListConfigs()
	}
	if err != nil {
		return mcTypes.SmartError(err)
	}

	return mcTypes.SyncResponse(true, configs)
}

func cmdConfigsPut(s mcTypes.State, r *http.Request) mcTypes.Response {
	var req types.Config
	configTable := ceph.GetConstConfigTable()

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	// Configure the key/value
	err = ceph.SetConfigItem(req)
	if err != nil {
		return mcTypes.SmartError(err)
	}

	if !req.SkipRestart {
		services := configTable[req.Key].Daemons
		err = configChangeRefresh(r.Context(), s, services, req.Wait)
		if err != nil {
			return mcTypes.InternalError(err)
		}
	}

	return mcTypes.EmptySyncResponse
}

func cmdConfigsDelete(s mcTypes.State, r *http.Request) mcTypes.Response {
	var req types.Config
	configTable := ceph.GetConstConfigTable()

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	// Clean the key/value
	err = ceph.RemoveConfigItem(req)
	if err != nil {
		return mcTypes.SmartError(err)
	}

	if !req.SkipRestart {
		services := configTable[req.Key].Daemons
		err = configChangeRefresh(r.Context(), s, services, req.Wait)
		if err != nil {
			return mcTypes.InternalError(err)
		}
	}

	return mcTypes.EmptySyncResponse
}

// Perform ordered (one after other) restart of provided Ceph services across the ceph cluster.
func configChangeRefresh(ctx context.Context, s mcTypes.State, services []string, wait bool) error {
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
			err := client.SendRestartRequestToClusterMembers(context.Background(), s, services)
			if err != nil {
				logger.Errorf("failed to send restart request to cluster members: %v", err)
			}
			err = ceph.RestartCephServices(context.Background(), interfaces.CephState{State: s}, services)
			if err != nil {
				logger.Errorf("failed to restart ceph services on current host: %v", err)
			}
		}()
	}

	return nil
}
