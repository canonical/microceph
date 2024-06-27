package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"
)

var clusterCmd = rest.Endpoint{
	Path: "cluster",
	Get:  rest.EndpointAction{Handler: cmdClusterGet, ProxyTarget: false},
}

// cmdClusterGet returns a json dump of the cluster config db.
func cmdClusterGet(s state.State, r *http.Request) response.Response {
	// fetch the cluster configurations from dqlite
	configs, err := ceph.GetConfigDb(r.Context(), interfaces.CephState{State: s})
	if err != nil {
		err := fmt.Errorf("failed to get config db: %w", err)
		logger.Error(err.Error())
		return response.InternalError(err)
	}

	data, err := json.Marshal(configs)
	if err != nil {
		err := fmt.Errorf("failed to marshal response data: %w", err)
		logger.Error(err.Error())
		return response.InternalError(err)
	}

	return response.SyncResponse(true, data)
}
