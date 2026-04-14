package api

import (
	"encoding/json"
	"net/http"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/interfaces"
)

var certificatesRGWCmd = mcTypes.Endpoint{
	Path: "certificates/rgw",
	Put:  mcTypes.EndpointAction{Handler: cmdCertificatesRGWPut, ProxyTarget: true},
}

func cmdCertificatesRGWPut(s mcTypes.State, r *http.Request) mcTypes.Response {
	var req types.CertificateSetRequest

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	err = ceph.UpdateRGWCertificates(interfaces.CephState{State: s}, req.SSLCertificate, req.SSLPrivateKey)
	if err != nil {
		return mcTypes.SmartError(err)
	}

	if req.Restart {
		err = ceph.RestartRGW()
		if err != nil {
			return mcTypes.SmartError(err)
		}
	}

	return mcTypes.EmptySyncResponse
}
