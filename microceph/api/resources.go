package api

import (
	"net/http"

	"github.com/canonical/lxd/lxd/resources"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
)

// /1.0/resources endpoint.
var resourcesCmd = mcTypes.Endpoint{
	Path: "resources",

	Get: mcTypes.EndpointAction{Handler: cmdResourcesGet, ProxyTarget: true},
}

func cmdResourcesGet(s mcTypes.State, r *http.Request) mcTypes.Response {
	storage, err := resources.GetStorage()
	if err != nil {
		return mcTypes.InternalError(err)
	}

	return mcTypes.SyncResponse(true, storage)
}
