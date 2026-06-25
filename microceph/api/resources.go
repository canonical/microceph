package api

import (
	"net/http"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/ceph"
)

// /1.0/resources endpoint.
var resourcesCmd = mcTypes.Endpoint{
	Path: "resources",

	Get: mcTypes.EndpointAction{Handler: cmdResourcesGet, ProxyTarget: true},
}

func cmdResourcesGet(s mcTypes.State, r *http.Request) mcTypes.Response {
	// GetStorage() can hit a transient TOCTOU race in /dev/disk/by-id (udevd
	// renames a .#-prefixed temp entry out from under the enumeration). Retry
	// so a `microceph disk list` / disk-remove verification poll does not fail
	// on a disappearing entry. See ceph.GetStorageWithRetry.
	storage, err := ceph.GetStorageWithRetry(ceph.StorageImpl{}.GetStorage)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	return mcTypes.SyncResponse(true, storage)
}
