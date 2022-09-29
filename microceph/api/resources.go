package api

import (
	"net/http"

	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/state"
	"github.com/lxc/lxd/lxd/resources"
	"github.com/lxc/lxd/lxd/response"
)

// /1.0/resources endpoint.
var resourcesCmd = rest.Endpoint{
	Path: "resources",

	Get: rest.EndpointAction{Handler: cmdResourcesGet},
}

func cmdResourcesGet(s *state.State, r *http.Request) response.Response {
	storage, err := resources.GetStorage()
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, storage)
}
