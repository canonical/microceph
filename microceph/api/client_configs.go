package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/canonical/microceph/microceph/contants"
	"github.com/canonical/microceph/microceph/interfaces"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/state"
)

// Top level client API
var clientCmd = rest.Endpoint{
	Path: "client",
}

// client configs API
var clientConfigsCmd = rest.Endpoint{
	Path: "client/configs",
	Put:  rest.EndpointAction{Handler: cmdClientConfigsPut, ProxyTarget: true},
	Get:  rest.EndpointAction{Handler: cmdClientConfigsGet, ProxyTarget: true},
}

// cmdClientConfigsGet fetches multiple client config key entries from internal database.
func cmdClientConfigsGet(s *state.State, r *http.Request) response.Response {
	var req types.ClientConfig
	var configs database.ClientConfigItems

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	if req.Host == contants.ClientConfigGlobalHostConst {
		configs, err = database.ClientConfigQuery.GetAll(s)
	} else {
		configs, err = database.ClientConfigQuery.GetAllForHost(s, req.Host)
	}
	if err != nil {
		logger.Errorf("failed fetching client configs: %v for %v", err, req)
		return response.SyncResponse(false, nil)
	}

	logger.Infof("Database Response: %v", configs)

	return response.SyncResponse(true, configs.GetClientConfigSlice())
}

// cmdClientConfigsPut renders .conf file at that particular host.
func cmdClientConfigsPut(s *state.State, r *http.Request) response.Response {
	// Check if microceph is bootstrapped.
	err := s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		isFsid, err := database.ConfigItemExists(ctx, tx, "fsid")
		if err != nil || !isFsid {
			return fmt.Errorf("client configuration cannot be performed before bootstrapping the cluster")
		}
		return nil
	})
	if err != nil {
		logger.Error(err.Error())
		return response.BadRequest(err)
	}

	err = ceph.UpdateConfig(interfaces.CephState{State: s})
	if err != nil {
		logger.Error(err.Error())
		response.InternalError(err)
	}

	return response.EmptySyncResponse
}

// client configs key API
var clientConfigsKeyCmd = rest.Endpoint{
	Path:   "client/configs/{key}",
	Put:    rest.EndpointAction{Handler: clientConfigsKeyPut, ProxyTarget: true},
	Get:    rest.EndpointAction{Handler: clientConfigsKeyGet, ProxyTarget: true},
	Delete: rest.EndpointAction{Handler: clientConfigsKeyDelete, ProxyTarget: true},
}

// clientConfigsKeyGet fetches particular client config key entries from internal db.
func clientConfigsKeyGet(s *state.State, r *http.Request) response.Response {
	var req types.ClientConfig
	var configs database.ClientConfigItems

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	if req.Host == contants.ClientConfigGlobalHostConst {
		configs, err = database.ClientConfigQuery.GetAllForKey(s, req.Key)
	} else {
		configs, err = database.ClientConfigQuery.GetAllForKeyAndHost(s, req.Key, req.Host)
	}
	if err != nil {
		logger.Errorf("failed fetching client configs: %v for %v", err, req)
		return response.InternalError(err)
	}

	logger.Infof("Database Response: %v", configs)

	return response.SyncResponse(true, configs.GetClientConfigSlice())
}

// clientConfigsKeyPut sets particular client config key.
func clientConfigsKeyPut(s *state.State, r *http.Request) response.Response {
	var req types.ClientConfig

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	// If new config request is for global configuration.
	err = database.ClientConfigQuery.AddNew(s, req.Key, req.Value, req.Host)
	if err != nil {
		return response.InternalError(err)
	}

	// Trigger /conf file update across cluster.
	clientConfigUpdate(s, req.Wait)

	return response.EmptySyncResponse
}

// clientConfigsKeyDelete removes particular client config key entries from internal db.
func clientConfigsKeyDelete(s *state.State, r *http.Request) response.Response {
	var req types.ClientConfig

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	if req.Host == contants.ClientConfigGlobalHostConst {
		err = database.ClientConfigQuery.RemoveAllForKey(s, req.Key)
	} else {
		err = database.ClientConfigQuery.RemoveOneForKeyAndHost(s, req.Key, req.Host)
	}
	if err != nil {
		return response.InternalError(err)
	}

	// Trigger /conf file update across cluster.
	clientConfigUpdate(s, req.Wait)

	return response.EmptySyncResponse
}

// clientConfigUpdate performs ordered (one after other) updation of ceph.conf across the ceph cluster.
func clientConfigUpdate(s *state.State, wait bool) error {
	if wait {
		// Execute update conf synchronously
		err := client.SendUpdateClientConfRequestToClusterMembers(interfaces.CephState{State: s})
		if err != nil {
			return err
		}

		// Update on current host.
		err = ceph.UpdateConfig(interfaces.CephState{State: s})
		if err != nil {
			return err
		}
	} else { // Execute update asynchronously
		go func() {
			client.SendUpdateClientConfRequestToClusterMembers(interfaces.CephState{State: s})
			ceph.UpdateConfig(interfaces.CephState{State: s}) // Restart on current host.
		}()
	}

	return nil
}
