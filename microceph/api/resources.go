package api

import (
	"net/http"

	"github.com/canonical/lxd/lxd/resources"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"
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
