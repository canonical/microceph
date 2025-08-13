package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/microceph/microceph/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"
	"github.com/gorilla/mux"
)

// remoteCmd is the top level remote endpoint.
var remoteCmd = rest.Endpoint{
	Path: "client/remotes",
	Get:  rest.EndpointAction{Handler: cmdRemoteGet, ProxyTarget: false},
}

// remoteNameCmd endpoint is for operations on specific remotes.
var remoteNameCmd = rest.Endpoint{
	Path:   "client/remotes/{name}",
	Put:    rest.EndpointAction{Handler: cmdRemotePut, ProxyTarget: false},
	Get:    rest.EndpointAction{Handler: cmdRemoteGet, ProxyTarget: false},
	Delete: rest.EndpointAction{Handler: cmdRemoteDelete, ProxyTarget: false},
}

// cmdRemotePut is handler for adding remote records to MicroCeph.
// This also triggers the $cluster file generation for all MicroCeph hosts.
func cmdRemotePut(state state.State, r *http.Request) response.Response {
	var req types.RemoteImportRequest

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	err = renderConfAndKeyringFiles(req.Name, req.LocalName, req.Config)
	if err != nil {
		return response.InternalError(fmt.Errorf("couldn't render files: %w", err))
	}

	if !req.RenderOnly {
		logger.Infof("REM: Sending remote(%s) info to cluster members.", req.Name)

		// Asynchronously persist this on db and send request to other cluster members.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*120)
			defer cancel()

			// Send render only request to remaining cluster members.
			req.RenderOnly = true
			err = client.SendRemoteImportToClusterMembers(ctx, state, req)
			if err != nil {
				logger.Errorf("REM: failed to forward request to cluster: %s", err.Error())
			}

			err := database.PersistRemoteDb(ctx, interfaces.CephState{State: state}, req)
			if err != nil {
				logger.Errorf("REM: failed to persiste remote: %s", err.Error())
			}

		}()
	}

	return response.EmptySyncResponse
}

// cmdRemoteGet is handler for fetching Remote records from MicroCeph internal db.
func cmdRemoteGet(state state.State, r *http.Request) response.Response {
	// PathUnescape will NOT fail if no name is provided in API request.
	// Additionally, remoteName in that case is initialised to "".
	remoteName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		logger.Errorf("REM: %v", err.Error())
		return response.InternalError(err)
	}

	remotes, err := database.GetRemoteDb(r.Context(), state, remoteName)
	if err != nil {
		return response.SmartError(err)
	}

	if len(remotes) == 0 {
		return response.SmartError(fmt.Errorf("no remotes configured"))
	}

	return response.SyncResponse(true, remotes)
}

// cmdRemoteDelete is handler for removing Remote records from MicroCeph internal db.
func cmdRemoteDelete(state state.State, r *http.Request) response.Response {
	remoteName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.BadRequest(err)
	}

	if isRemoteConfigured(remoteName) {
		return response.SmartError(fmt.Errorf("cannot remote remote(%s), disable RBD mirroring", remoteName))
	}

	// Remove remote record.
	err = database.DeleteRemoteDb(r.Context(), state, remoteName)
	if err != nil {
		return response.SmartError(err)
	}

	// Distrust the remote ceph user, and remove key.
	err = ceph.DeleteClientKey(remoteName)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

/*****************HELPER FUNCTIONS**************************/

func isRemoteConfigured(remoteName string) bool {
	// check remote configured for RBD mirroring
	return ceph.IsRemoteConfiguredForRbdMirror(remoteName)
}

// renderConfAndKeyringFiles generates the $cluster.conf and $cluster.keyring files on the host.
func renderConfAndKeyringFiles(remoteName string, localName string, configs map[string]string) error {
	monHosts := []string{}
	for k, v := range configs {
		if strings.Contains(k, "mon.host.") {
			monHosts = append(monHosts, v)
		}
	}

	confFileName := remoteName + ".conf"
	keyringFileName := remoteName + ".keyring"

	// Populate Template
	err := ceph.NewCephConfig(confFileName).WriteConfig(
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
		return err
	}

	err = ceph.NewCephKeyring(constants.GetPathConst().ConfPath, keyringFileName).WriteConfig(
		map[string]any{
			// Local cluster is the client saving remote cluster keyring.
			"name": fmt.Sprintf("client.%s", localName),
			"key":  configs[fmt.Sprintf(constants.AdminKeyringTemplate, localName)],
		},
		0640,
	)
	if err != nil {
		return err
	}

	return nil
}
