package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"
)

var clusterCmd = rest.Endpoint{
	Path: "cluster",
	Get:  rest.EndpointAction{Handler: cmdClusterGet, ProxyTarget: false},
}

// cmdClusterGet returns a json dump of microceph configs suitable for connecting from a remote cluster
// This also creates a new key based on the remote name with admin privs.
func cmdClusterGet(s state.State, r *http.Request) response.Response {
	// Fetch request params.
	var req types.ClusterExportRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	// Check that the cluster name is conformant.
	isOk, err := regexp.MatchString(constants.ClusterNameRegex, req.RemoteName)
	if err != nil || !isOk {
		err := fmt.Errorf("cluster names can only have [a-z] or [0-9] characters: %w", err)
		logger.Error(err.Error())
		return response.BadRequest(err)
	}

	// fetch the cluster configurations from dqlite
	configs, err := ceph.GetConfigDb(r.Context(), interfaces.CephState{State: s})
	if err != nil {
		err := fmt.Errorf("failed to get config db: %w", err)
		logger.Error(err.Error())
		return response.InternalError(err)
	}

	// generate client keys
	clientKey, err := ceph.CreateClientKey(
		req.RemoteName,
		[]string{"mon", "allow *"},
		[]string{"osd", "allow *"},
		[]string{"mds", "allow *"},
		[]string{"mgr", "allow *"},
	)
	if err != nil {
		return response.InternalError(err)
	}

	// replace admin key with remote client key.
	delete(configs, constants.AdminKeyringFieldName)
	configs[fmt.Sprintf(constants.AdminKeyringTemplate, req.RemoteName)] = clientKey

	data, err := json.Marshal(configs)
	if err != nil {
		err := fmt.Errorf("failed to marshal response data: %w", err)
		logger.Error(err.Error())
		return response.InternalError(err)
	}

	return response.SyncResponse(true, data)
}
