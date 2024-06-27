package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/state"
)

var remoteCmd = rest.Endpoint{
	Path: "remote",
	Put:  rest.EndpointAction{Handler: cmdRemoteGet, ProxyTarget: true},
}

func cmdRemoteGet(state *state.State, r *http.Request) response.Response {
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
			"name": "client.admin",
			"key":  configs["keyring.client.admin"],
		},
		0640,
	)
	if err != nil {
		return response.InternalError(fmt.Errorf("couldn't render %s: %w", keyringFileName, err))
	}

	return response.EmptySyncResponse
}
