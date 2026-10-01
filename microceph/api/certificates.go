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

	// UpdateRGWCertificates handles the --restart flag itself: the restart is
	// part of the same serialized lifecycle, so a failure cannot leave a
	// published-but-unservable certificate behind. Input failures (bad base64,
	// mismatched pair) carry ceph.ErrRgwFrontendInvalid and map to 400;
	// operational failures map to 500 (see serviceErrorResponse).
	err = ceph.UpdateRGWCertificates(r.Context(), interfaces.CephState{State: s}, req.SSLCertificate, req.SSLPrivateKey, req.Restart)
	if err != nil {
		return serviceErrorResponse(err)
	}

	return mcTypes.EmptySyncResponse
}
