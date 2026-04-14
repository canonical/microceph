package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/logger"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
)

// Top level client API
var clientCmd = mcTypes.Endpoint{
	Path: "client",
}

// client configs API
var clientConfigsCmd = mcTypes.Endpoint{
	Path: "client/configs",
	Put:  mcTypes.EndpointAction{Handler: cmdClientConfigsPut, ProxyTarget: true},
	Get:  mcTypes.EndpointAction{Handler: cmdClientConfigsGet, ProxyTarget: true},
}

// cmdClientConfigsGet fetches multiple client config key entries from internal database.
func cmdClientConfigsGet(s mcTypes.State, r *http.Request) mcTypes.Response {
	var req types.ClientConfig
	var configs database.ClientConfigItems

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	if req.Host == constants.ClientConfigGlobalHostConst {
		configs, err = database.ClientConfigQuery.GetAll(r.Context(), s)
	} else {
		configs, err = database.ClientConfigQuery.GetAllForHost(r.Context(), s, req.Host)
	}
	if err != nil {
		logger.Errorf("failed fetching client configs: %v for %v", err, req)
		return mcTypes.SyncResponse(false, nil)
	}

	logger.Infof("Database Response: %v", configs)

	return mcTypes.SyncResponse(true, configs.GetClientConfigSlice())
}

// cmdClientConfigsPut renders .conf file at that particular host.
func cmdClientConfigsPut(s mcTypes.State, r *http.Request) mcTypes.Response {
	// Check if microceph is bootstrapped.
	err := s.Database().Transaction(r.Context(), func(ctx context.Context, tx *sql.Tx) error {
		isFsid, err := database.ConfigItemExists(ctx, tx, "fsid")
		if err != nil || !isFsid {
			return fmt.Errorf("client configuration cannot be performed before bootstrapping the cluster")
		}
		return nil
	})
	if err != nil {
		logger.Error(err.Error())
		return mcTypes.BadRequest(err)
	}

	err = ceph.UpdateConfig(r.Context(), interfaces.CephState{State: s})
	if err != nil {
		logger.Error(err.Error())
		mcTypes.InternalError(err)
	}

	return mcTypes.EmptySyncResponse
}

// client configs key API
var clientConfigsKeyCmd = mcTypes.Endpoint{
	Path:   "client/configs/{key}",
	Put:    mcTypes.EndpointAction{Handler: clientConfigsKeyPut, ProxyTarget: true},
	Get:    mcTypes.EndpointAction{Handler: clientConfigsKeyGet, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: clientConfigsKeyDelete, ProxyTarget: true},
}

// clientConfigsKeyGet fetches particular client config key entries from internal db.
func clientConfigsKeyGet(s mcTypes.State, r *http.Request) mcTypes.Response {
	var req types.ClientConfig
	var configs database.ClientConfigItems

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	if req.Host == constants.ClientConfigGlobalHostConst {
		configs, err = database.ClientConfigQuery.GetAllForKey(r.Context(), s, req.Key)
	} else {
		configs, err = database.ClientConfigQuery.GetAllForKeyAndHost(r.Context(), s, req.Key, req.Host)
	}
	if err != nil {
		logger.Errorf("failed fetching client configs: %v for %v", err, req)
		return mcTypes.InternalError(err)
	}

	logger.Infof("Database Response: %v", configs)

	return mcTypes.SyncResponse(true, configs.GetClientConfigSlice())
}

// clientConfigsKeyPut sets particular client config key.
func clientConfigsKeyPut(s mcTypes.State, r *http.Request) mcTypes.Response {
	var req types.ClientConfig

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	// If new config request is for global configuration.
	err = database.ClientConfigQuery.AddNew(r.Context(), s, req.Key, req.Value, req.Host)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	// Trigger /conf file update across cluster.
	err = clientConfigUpdate(r.Context(), s, req.Wait)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	return mcTypes.EmptySyncResponse
}

// clientConfigsKeyDelete removes particular client config key entries from internal db.
func clientConfigsKeyDelete(s mcTypes.State, r *http.Request) mcTypes.Response {
	var req types.ClientConfig

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	if req.Host == constants.ClientConfigGlobalHostConst {
		err = database.ClientConfigQuery.RemoveAllForKey(r.Context(), s, req.Key)
	} else {
		err = database.ClientConfigQuery.RemoveOneForKeyAndHost(r.Context(), s, req.Key, req.Host)
	}
	if err != nil {
		return mcTypes.InternalError(err)
	}

	// Trigger /conf file update across cluster.
	err = clientConfigUpdate(r.Context(), s, req.Wait)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	return mcTypes.EmptySyncResponse
}

// clientConfigUpdate performs ordered (one after other) updation of ceph.conf across the ceph cluster.
func clientConfigUpdate(ctx context.Context, s mcTypes.State, wait bool) error {
	if wait {
		// Execute update conf synchronously
		err := client.SendUpdateClientConfRequestToClusterMembers(ctx, interfaces.CephState{State: s})
		if err != nil {
			return err
		}

		// Update on current host.
		err = ceph.UpdateConfig(ctx, interfaces.CephState{State: s})
		if err != nil {
			return err
		}
	} else { // Execute update asynchronously
		go func() {
			err := client.SendUpdateClientConfRequestToClusterMembers(context.Background(), interfaces.CephState{State: s})
			if err != nil {
				logger.Errorf("failed to send client conf update to cluster members: %v", err)
			}
			err = ceph.UpdateConfig(context.Background(), interfaces.CephState{State: s})
			if err != nil {
				logger.Errorf("failed to update config on current host: %v", err)
			}
		}()
	}

	return nil
}
