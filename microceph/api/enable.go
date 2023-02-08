package api

import (
	"encoding/json"
	"github.com/canonical/microceph/microceph/common"
	"net/http"

	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/state"
	"github.com/lxc/lxd/lxd/response"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
)

// /1.0/enable/rgw endpoint.
var enableRGWCmd = rest.Endpoint{
	Path: "enable/rgw",

	Post: rest.EndpointAction{Handler: cmdEnableRGWPost, ProxyTarget: true},
}

// cmdEnableRGWPost is the handler for POST /1.0/enable/rgw.
func cmdEnableRGWPost(s *state.State, r *http.Request) response.Response {
	var req types.EnableRGWPost

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	err = ceph.EnableRGW(common.CephState{State: s}, req.Port)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
