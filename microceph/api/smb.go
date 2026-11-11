package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

var smbServiceCmd = mcTypes.Endpoint{
	Path:   "services/smb",
	Get:    mcTypes.EndpointAction{Handler: cmdSMBGet, ProxyTarget: true},
	Put:    mcTypes.EndpointAction{Handler: cmdSMBPut, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: cmdSMBDelete, ProxyTarget: true},
}

func cmdSMBGet(s mcTypes.State, r *http.Request) mcTypes.Response {
	status, err := ceph.SMBLocalStatus(r.Context(), interfaces.CephState{State: s})
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

var retireSMBOrchestratedFunc = ceph.RetireOrchestratedSMB

func cmdSMBDelete(s mcTypes.State, r *http.Request) mcTypes.Response {
	var payload types.SMBService
	err := json.NewDecoder(r.Body).Decode(&payload)
	if err != nil {
		return mcTypes.SmartError(err)
	}
	if payload.Orchestrated {
		if payload.Purge {
			return mcTypes.BadRequest(fmt.Errorf("orch retirement cannot purge identity"))
		}
		// The SMB module may hold its store lock while invoking remove_service.
		// Native work must not keep that callback waiting on serviceStartMu.
		requestCtx := context.WithoutCancel(r.Context())
		go func() {
			workCtx, cancel := context.WithTimeout(requestCtx, 5*time.Minute)
			defer cancel()
			err := retireSMBOrchestratedFunc(workCtx, interfaces.CephState{State: s}, payload.ClusterID)
			if err != nil {
				logger.Warnf("SMB orch retirement failed for %s: %v", payload.ClusterID, err)
			}
		}()
		return mcTypes.EmptySyncResponse
	}
	err = ceph.DisableSMB(r.Context(), interfaces.CephState{State: s}, payload.ClusterID, payload.Purge)
	if err != nil {
		return mcTypes.SmartError(err)
	}
	return mcTypes.EmptySyncResponse
}
