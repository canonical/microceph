package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"

	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/state"

	"github.com/canonical/microceph/microceph/ceph"
)

// /1.0/services endpoint.
var servicesCmd = rest.Endpoint{
	Path: "services",

	Get: rest.EndpointAction{Handler: cmdServicesGet, ProxyTarget: true},
}

func cmdServicesGet(s state.State, r *http.Request) response.Response {
	services, err := ceph.ListServices(r.Context(), s)
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, services)
}

// Service endpoints.
var monServiceCmd = rest.Endpoint{
	Path:   "services/mon",
	Get:    rest.EndpointAction{Handler: cmdMonGet, ProxyTarget: true},
	Put:    rest.EndpointAction{Handler: cmdEnableServicePut, ProxyTarget: true},
	Delete: rest.EndpointAction{Handler: cmdDeleteService, ProxyTarget: true},
}
var mgrServiceCmd = rest.Endpoint{
	Path:   "services/mgr",
	Put:    rest.EndpointAction{Handler: cmdEnableServicePut, ProxyTarget: true},
	Delete: rest.EndpointAction{Handler: cmdDeleteService, ProxyTarget: true},
}
var mdsServiceCmd = rest.Endpoint{
	Path:   "services/mds",
	Put:    rest.EndpointAction{Handler: cmdEnableServicePut, ProxyTarget: true},
	Delete: rest.EndpointAction{Handler: cmdDeleteService, ProxyTarget: true},
}
var nfsServiceCmd = rest.Endpoint{
	Path:   "services/nfs",
	Put:    rest.EndpointAction{Handler: cmdEnableServicePut, ProxyTarget: true},
	Delete: rest.EndpointAction{Handler: cmdNFSDeleteService, ProxyTarget: true},
}
var rgwServiceCmd = rest.Endpoint{
	Path:   "services/rgw",
	Put:    rest.EndpointAction{Handler: cmdEnableServicePut, ProxyTarget: true},
	Delete: rest.EndpointAction{Handler: cmdRGWServiceDelete, ProxyTarget: true},
}
var rbdMirroServiceCmd = rest.Endpoint{
	Path:   "services/rbd-mirror",
	Put:    rest.EndpointAction{Handler: cmdEnableServicePut, ProxyTarget: true},
	Delete: rest.EndpointAction{Handler: cmdDeleteService, ProxyTarget: true},
}

// cmdMonGet returns the mon service status.
func cmdMonGet(s state.State, r *http.Request) response.Response {

	// fetch monitor addresses
	monitors, err := ceph.GetMonitorAddresses(r.Context(), interfaces.CephState{State: s})
	if err != nil {
		return response.InternalError(err)
	}

	monStatus := types.MonitorStatus{Addresses: monitors}

	return response.SyncResponse(true, monStatus)

}

func cmdEnableServicePut(s state.State, r *http.Request) response.Response {
	var payload types.EnableService

	err := json.NewDecoder(r.Body).Decode(&payload)
	if err != nil {
		logger.Errorf("Failed decoding enable service request: %v", err)
		return response.InternalError(err)
	}

	err = ceph.ServicePlacementHandler(r.Context(), interfaces.CephState{State: s}, payload)
	if err != nil {
		return response.SyncResponse(false, err)
	}

	return response.SyncResponse(true, nil)
}

// Service Reload Endpoint.
var restartServiceCmd = rest.Endpoint{
	Path: "services/restart",
	Post: rest.EndpointAction{Handler: cmdRestartServicePost, ProxyTarget: true},
}

func cmdRestartServicePost(s state.State, r *http.Request) response.Response {
	var services types.Services

	err := json.NewDecoder(r.Body).Decode(&services)
	if err != nil {
		logger.Errorf("Failed decoding restart services: %v", err)
		return response.InternalError(err)
	}

	// Check if provided services are valid and available in microceph
	for _, service := range services {
		valid_services := ceph.GetConfigTableServiceSet()
		if _, ok := valid_services[service.Service]; !ok {
			err := fmt.Errorf("%s is not a valid ceph service", service.Service)
			logger.Errorf("%v", err)
			return response.InternalError(err)
		}
	}

	clusterServices, err := ceph.ListServices(r.Context(), s)
	if err != nil {
		logger.Errorf("failed fetching services from db: %v", err)
		return response.SyncResponse(false, err)
	}

	for _, service := range services {
		err = ceph.RestartCephService(clusterServices, service.Service, s.Name())
		if err != nil {
			url := s.Address().String()
			logger.Errorf("Failed restarting %s on host %s", service.Service, url)
			return response.SyncResponse(false, err)
		}
	}

	return response.EmptySyncResponse
}

// cmdDeleteService handles service deletion.
func cmdDeleteService(s state.State, r *http.Request) response.Response {
	which := path.Base(r.URL.Path)
	_, ok := ceph.GetConfigTableServiceSet()[which]
	if !ok {
		err := fmt.Errorf("%s is not a valid ceph service", which)
		logger.Errorf("%v", err)
		return response.InternalError(err)
	}

	err := ceph.DeleteService(r.Context(), interfaces.CephState{State: s}, which)
	if err != nil {
		return response.SyncResponse(false, err)
	}

	return response.SyncResponse(true, nil)
}

// cmdNFSDeleteService handles the NFS service deletion.
func cmdNFSDeleteService(s state.State, r *http.Request) response.Response {
	var svc types.NFSService

	err := json.NewDecoder(r.Body).Decode(&svc)
	if err != nil {
		logger.Errorf("Failed decoding disable service request: %v", err)
		return response.InternalError(err)
	}

	if len(svc.ClusterID) == 0 {
		err := fmt.Errorf("Expected cluster_id to not be empty.")
		return response.SmartError(err)
	}

	err = ceph.DisableNFS(r.Context(), interfaces.CephState{State: s}, svc.ClusterID)
	if err != nil {
		logger.Errorf("Failed disabling NFS: %v", err)
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func cmdRGWServiceDelete(s state.State, r *http.Request) response.Response {
	err := ceph.DisableRGW(r.Context(), interfaces.CephState{State: s})
	if err != nil {
		logger.Errorf("Failed disabling RGW: %v", err)
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
