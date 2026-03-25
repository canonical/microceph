package api

import (
	"encoding/json"
	"net/http"

	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/lxd/lxd/response"
)

var certificatesRGWCmd = rest.Endpoint{
	Path: "certificates/rgw",
	Put:  rest.EndpointAction{Handler: cmdCertificatesRGWPut, ProxyTarget: true},
}

func cmdCertificatesRGWPut(s state.State, r *http.Request) response.Response {
	var req types.CertificateSetRequest

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.InternalError(err)
	}

	err = ceph.UpdateRGWCertificates(interfaces.CephState{State: s}, req.SSLCertificate, req.SSLPrivateKey)
	if err != nil {
		return response.SmartError(err)
	}

	if req.Restart {
		err = ceph.RestartRGW()
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.EmptySyncResponse
}
