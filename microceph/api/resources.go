package api

import (
	"net/http"

	"github.com/canonical/lxd/lxd/resources"
	"github.com/canonical/lxd/lxd/response"

	"github.com/canonical/microceph/microceph/api/rest"
	state "github.com/canonical/microceph/microceph/clusterstate"
)

// /1.0/resources endpoint.
var resourcesCmd = rest.Endpoint{
	Path: "resources",

	Get: rest.EndpointAction{Handler: cmdResourcesGet, ProxyTarget: true},
}

func cmdResourcesGet(s *state.State, r *http.Request) response.Response {
	storage, err := resources.GetStorage()
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, storage)
}
