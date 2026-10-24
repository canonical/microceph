package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/interfaces"
)

var smbServiceCmd = mcTypes.Endpoint{
	Path:   "services/smb",
	Get:    mcTypes.EndpointAction{Handler: cmdSMBGet, ProxyTarget: true},
	Put:    mcTypes.EndpointAction{Handler: cmdSMBPut, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: cmdSMBDelete, ProxyTarget: true},
}

func cmdSMBGet(s mcTypes.State, r *http.Request) mcTypes.Response {
	status, err := ceph.SMBLocalStatus(r.Context())
	if err != nil {
		return mcTypes.SmartError(err)
	}
	return mcTypes.SyncResponse(true, status)
}

func cmdSMBPut(s mcTypes.State, r *http.Request) mcTypes.Response {
	var payload types.EnableService
	err := json.NewDecoder(r.Body).Decode(&payload)
	if err != nil {
		return mcTypes.SmartError(err)
	}
	if payload.Name != "smb" {
		return mcTypes.SmartError(fmt.Errorf("SMB endpoint requires smb service payload"))
	}
	err = ceph.ServicePlacementHandler(r.Context(), interfaces.CephState{State: s}, payload)
	if err != nil {
		return mcTypes.SmartError(err)
	}
	return mcTypes.EmptySyncResponse
}

func cmdSMBDelete(s mcTypes.State, r *http.Request) mcTypes.Response {
	var payload types.SMBService
	err := json.NewDecoder(r.Body).Decode(&payload)
	if err != nil {
		return mcTypes.SmartError(err)
	}
	err = ceph.DisableSMB(r.Context(), interfaces.CephState{State: s}, payload.ClusterID, payload.Purge)
	if err != nil {
		return mcTypes.SmartError(err)
	}
	return mcTypes.EmptySyncResponse
}
