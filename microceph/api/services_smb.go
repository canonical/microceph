package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
)

// /1.0/services/smb endpoint: cluster-scoped SMBSpec operations, the
// mgr/smb -> microceph-orch contract (apply/remove/status).
var smbServiceCmd = mcTypes.Endpoint{
	Path:   "services/smb",
	Get:    mcTypes.EndpointAction{Handler: cmdSMBServiceGet, ProxyTarget: true},
	Put:    mcTypes.EndpointAction{Handler: cmdSMBServicePut, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: cmdSMBServiceDelete, ProxyTarget: true},
}

// /1.0/services/smb/node endpoint: node-scoped enable/disable used by the
// cluster-level fan-out (invoked with UseTarget per placed node).
var smbNodeServiceCmd = mcTypes.Endpoint{
	Path:   "services/smb/node",
	Put:    mcTypes.EndpointAction{Handler: cmdEnableServicePut, ProxyTarget: true},
	Post:   mcTypes.EndpointAction{Handler: cmdSMBNodePost, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: cmdSMBNodeDelete, ProxyTarget: true},
}

// cmdSMBNodePost regenerates this node's smb configs from the stored
// spec and restarts ctdbd.
func cmdSMBNodePost(s mcTypes.State, r *http.Request) mcTypes.Response {
	var svc types.SMBService

	err := json.NewDecoder(r.Body).Decode(&svc)
	if err != nil {
		logger.Errorf("failed decoding smb node regenerate request: %v", err)
		return mcTypes.InternalError(err)
	}

	err = ceph.RegenerateSMBNode(r.Context(), interfaces.CephState{State: s}, svc.ClusterID)
	if err != nil {
		logger.Errorf("failed regenerating smb on node: %v", err)
		return mcTypes.SmartError(err)
	}

	return mcTypes.EmptySyncResponse
}

// cmdSMBServiceGet lists every smb cluster with its spec and placement.
func cmdSMBServiceGet(s mcTypes.State, r *http.Request) mcTypes.Response {
	statuses, err := ceph.ListSMB(r.Context(), interfaces.CephState{State: s})
	if err != nil {
		return mcTypes.InternalError(err)
	}

	return mcTypes.SyncResponse(true, statuses)
}

// cmdSMBServicePut applies an SMBSpec (JSON body) to the cluster.
func cmdSMBServicePut(s mcTypes.State, r *http.Request) mcTypes.Response {
	// SMBSpecs are small; the limit only guards against runaway bodies.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		logger.Errorf("failed reading smb spec body: %v", err)
		return mcTypes.InternalError(err)
	}

	err = ceph.ApplySMB(r.Context(), interfaces.CephState{State: s}, string(body))
	if err != nil {
		logger.Errorf("failed applying smb spec: %v", err)
		if errors.Is(err, ceph.ErrInvalidSMBSpec) {
			return mcTypes.BadRequest(err)
		}
		return mcTypes.SmartError(err)
	}

	return mcTypes.EmptySyncResponse
}

// cmdSMBServiceDelete removes an smb cluster from all its member nodes.
func cmdSMBServiceDelete(s mcTypes.State, r *http.Request) mcTypes.Response {
	var svc types.SMBService

	err := json.NewDecoder(r.Body).Decode(&svc)
	if err != nil {
		logger.Errorf("failed decoding smb delete request: %v", err)
		return mcTypes.InternalError(err)
	}

	if !types.SMBClusterIDRegex.MatchString(svc.ClusterID) {
		err := errors.New("expected cluster_id to be valid (regex: '" + types.SMBClusterIDRegex.String() + "')")
		return mcTypes.BadRequest(err)
	}

	err = ceph.RemoveSMB(r.Context(), interfaces.CephState{State: s}, svc.ClusterID)
	if err != nil {
		logger.Errorf("failed removing smb cluster '%s': %v", svc.ClusterID, err)
		return mcTypes.SmartError(err)
	}

	return mcTypes.EmptySyncResponse
}

// cmdSMBNodeDelete tears down smb cluster membership on this node.
func cmdSMBNodeDelete(s mcTypes.State, r *http.Request) mcTypes.Response {
	var svc types.SMBService

	err := json.NewDecoder(r.Body).Decode(&svc)
	if err != nil {
		logger.Errorf("failed decoding smb node delete request: %v", err)
		return mcTypes.InternalError(err)
	}

	err = ceph.DisableSMB(r.Context(), interfaces.CephState{State: s}, svc.ClusterID)
	if err != nil {
		logger.Errorf("failed disabling smb on node: %v", err)
		return mcTypes.SmartError(err)
	}

	return mcTypes.EmptySyncResponse
}
