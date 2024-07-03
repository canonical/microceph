package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/state"
)

var clusterCmd = rest.Endpoint{
	Path: "cluster",
	Get:  rest.EndpointAction{Handler: cmdClusterGet, ProxyTarget: false},
}

// cmdClusterGet returns a json dump of the cluster config db.
var cmdClusterGet = func(s *state.State, r *http.Request) response.Response {
	// Fetch request params.
	var req types.ClusterStateRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	// fetch the cluster configurations from dqlite
	configs, err := ceph.GetConfigDb(interfaces.CephState{State: s})
	if err != nil {
		err := fmt.Errorf("failed to get config db: %w", err)
		logger.Error(err.Error())
		return response.InternalError(err)
	}

	// generate client keys
	clientKey, err := ceph.CreateKey(
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
