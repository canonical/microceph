package api

import (
	"encoding/json"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/state"
)

// 1.0/ops endpoint
var opsCmd = rest.Endpoint{
	Path: "ops",
}

// 1.0/ops/bootstrap
var opsBootstrapCmd = rest.Endpoint{
	Path:              "ops/bootstrap",
	AllowedBeforeInit: true,
	Post:              rest.EndpointAction{Handler: cmdBoostrapPost, ProxyTarget: false},
}

// cmdBoostrapPost bootstraps MicroCeph.
func cmdBoostrapPost(s *state.State, r *http.Request) response.Response {
	var data types.Bootstrap

	err := json.NewDecoder(r.Body).Decode(&data)
	if err != nil {
		logger.Errorf("Failed decoding ceph bootstrap request: %v", err)
		return response.InternalError(err)
	}

	err = ceph.Bootstrap(common.CephState{State: s}, data)
	if err != nil {
		return response.SyncResponse(false, err)
	}

	return response.EmptySyncResponse
}
