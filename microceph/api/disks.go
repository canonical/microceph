package api

import (
	"encoding/json"
	"net/http"

	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/state"
	"github.com/lxc/lxd/lxd/response"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
)

// /1.0/disks endpoint.
var disksCmd = rest.Endpoint{
	Path: "disks",

	Get:  rest.EndpointAction{Handler: cmdDisksGet, ProxyTarget: true},
	Post: rest.EndpointAction{Handler: cmdDisksPost, ProxyTarget: true},
}

func cmdDisksGet(s *state.State, r *http.Request) response.Response {
	disks, err := ceph.ListOSD(s)
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, disks)
}

func cmdDisksPost(s *state.State, r *http.Request) response.Response {
	var req types.DisksPost

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	err = ceph.AddOSD(s, req.Path, req.Wipe)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
