package api

import (
	"net/http"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
)

// CapabilitiesSupported lists the CE142 snap capability/API-extension markers
// that the charm can check to determine whether role-managed placement is
// supported by the running snap revision.
var CapabilitiesSupported = []string{
	"deferred-ceph-bootstrap",
	"ceph-only-bootstrap",
	"declarative-placement",
	// placement-rgw: object-shaped rgw placement (enabled/port/ssl_port/SSL
	// material) plus an observed rgw_frontend in GET /placement (CE142 Option B).
	// The charm gates role-managed RGW on this marker; a snap without it would
	// 400 on the object payload via DisallowUnknownFields.
	"placement-rgw",
}

// capabilitiesCmd is the cluster capabilities endpoint (CE142).
var capabilitiesCmd = mcTypes.Endpoint{
	Path: "cluster/capabilities",
	Get:  mcTypes.EndpointAction{Handler: cmdCapabilitiesGet, ProxyTarget: false},
}

// cmdCapabilitiesGet returns the list of supported capability markers.
func cmdCapabilitiesGet(_ mcTypes.State, _ *http.Request) mcTypes.Response {
	return mcTypes.SyncResponse(true, types.Capabilities{
		Supported: CapabilitiesSupported,
	})
}
