package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/state"
	"github.com/gorilla/mux"
)

var remoteCmd = rest.Endpoint{
	Path: "client/remotes",
	Get:  rest.EndpointAction{Handler: CmdRemoteGet, ProxyTarget: false},
}

var remoteNameCmd = rest.Endpoint{
	Path:   "client/remotes/{name}",
	Put:    rest.EndpointAction{Handler: CmdRemotePut, ProxyTarget: false},
	Get:    rest.EndpointAction{Handler: CmdRemoteGet, ProxyTarget: false},
	Delete: rest.EndpointAction{Handler: CmdRemoteDelete, ProxyTarget: false},
}

var CmdRemotePut = func(state *state.State, r *http.Request) response.Response {
	var req types.Remote

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	configs := req.Config
	monHosts := []string{}
	for k, v := range configs {
		if strings.Contains(k, "mon.host.") {
			monHosts = append(monHosts, v)
		}
	}

	confFileName := req.Name + ".conf"
	keyringFileName := req.Name + ".keyring"

	// Populate Template
	err = ceph.NewCephConfig(confFileName).WriteConfig(
		map[string]any{
			"fsid":     configs["fsid"],
			"monitors": strings.Join(monHosts, ","),
			"pubNet":   configs["public_network"],
			"ipv4":     strings.Contains(configs["public_network"], "."),
			"ipv6":     strings.Contains(configs["public_network"], ":"),
		},
		0644,
	)
	if err != nil {
		return response.InternalError(fmt.Errorf("couldn't render %s: %w", confFileName, err))
	}

	err = ceph.NewCephKeyring(constants.GetPathConst().ConfPath, keyringFileName).WriteConfig(
		map[string]any{
			// Local cluster is the client saving remote cluster keyring.
			"name": fmt.Sprintf("client.%s", req.LocalName),
			"key":  configs[fmt.Sprintf(constants.AdminKeyringTemplate, req.LocalName)],
		},
		0640,
	)
	if err != nil {
		return response.InternalError(fmt.Errorf("couldn't render %s: %w", keyringFileName, err))
	}

	// Asynchronously persist this on db.
	go func() {
		err := PersisteRemoteAndConfigs(interfaces.CephState{State: state}, req)
		if err != nil {
			logger.Errorf("failed to persiste remote: %s", err.Error())
		}
	}()

	return response.EmptySyncResponse
}

var CmdRemoteGet = func(state *state.State, r *http.Request) response.Response {
	remoteName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err == nil {
		remoteName = ""
	}

	remotes, err := database.GetRemoteDb(*state, remoteName)
	if err != nil {
		return response.SmartError(err)
	}

	if len(remotes) == 0 {
		return response.SmartError(fmt.Errorf("no remotes configured"))
	}

	return response.SyncResponse(true, remotes)
}

var CmdRemoteDelete = func(state *state.State, r *http.Request) response.Response {
	remoteName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.BadRequest(err)
	}

	err = database.DeleteRemoteDb(*state, remoteName)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

/*****************HELPER FUNCTIONS**************************/
// PersisteRemoteAndConfigs adds the remote record  to dqlite.
var PersisteRemoteAndConfigs = func(s interfaces.StateInterface, remote types.Remote) error {
	err := s.ClusterState().Database.Transaction(s.ClusterState().Context, func(ctx context.Context, tx *sql.Tx) error {
		// Record the remote.
		_, err := database.CreateRemote(ctx, tx, database.Remote{LocalName: remote.LocalName, Name: remote.Name})
		if err != nil {
			return fmt.Errorf("failed to record remote %s: %w", remote.Name, err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}
