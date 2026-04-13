package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/logger"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/ceph"
)

// /1.0/services endpoint.
var servicesCmd = mcTypes.Endpoint{
	Path: "services",

	Get: mcTypes.EndpointAction{Handler: cmdServicesGet, ProxyTarget: true},
}

func cmdServicesGet(s mcTypes.State, r *http.Request) mcTypes.Response {
	services, err := database.ServiceQuery.List(r.Context(), s)
	if err != nil {
		return mcTypes.InternalError(err)
	}

	groupedServices, err := database.GroupedServicesQuery.GetGroupedServices(r.Context(), interfaces.CephState{State: s})
	if err != nil {
		return mcTypes.InternalError(err)
	}

	for _, groupedService := range groupedServices {
		services = append(services, types.Service{
			Service:  groupedService.Service,
			Location: groupedService.Member,
			GroupID:  groupedService.GroupID,
			Info:     groupedService.Info,
		})
	}

	return mcTypes.SyncResponse(true, services)
}

// Service endpoints.
var monServiceCmd = mcTypes.Endpoint{
	Path:   "services/mon",
	Get:    mcTypes.EndpointAction{Handler: cmdMonGet, ProxyTarget: true},
	Put:    mcTypes.EndpointAction{Handler: cmdEnableServicePut, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: cmdDeleteService, ProxyTarget: true},
}
var mgrServiceCmd = mcTypes.Endpoint{
	Path:   "services/mgr",
	Put:    mcTypes.EndpointAction{Handler: cmdEnableServicePut, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: cmdDeleteService, ProxyTarget: true},
}
var mdsServiceCmd = mcTypes.Endpoint{
	Path:   "services/mds",
	Put:    mcTypes.EndpointAction{Handler: cmdEnableServicePut, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: cmdDeleteService, ProxyTarget: true},
}
var nfsServiceCmd = mcTypes.Endpoint{
	Path:   "services/nfs",
	Put:    mcTypes.EndpointAction{Handler: cmdEnableServicePut, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: cmdNFSDeleteService, ProxyTarget: true},
}
var rgwServiceCmd = mcTypes.Endpoint{
	Path:   "services/rgw",
	Put:    mcTypes.EndpointAction{Handler: cmdEnableServicePut, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: cmdRGWServiceDelete, ProxyTarget: true},
}
var rbdMirroServiceCmd = mcTypes.Endpoint{
	Path:   "services/rbd-mirror",
	Put:    mcTypes.EndpointAction{Handler: cmdEnableServicePut, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: cmdDeleteService, ProxyTarget: true},
}
var fsMirroServiceCmd = mcTypes.Endpoint{
	Path:   "services/cephfs-mirror",
	Put:    mcTypes.EndpointAction{Handler: cmdEnableServicePut, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: cmdDeleteService, ProxyTarget: true},
}

// cmdMonGet returns the mon service status.
func cmdMonGet(s mcTypes.State, r *http.Request) mcTypes.Response {

	// fetch monitor addresses
	monitors, err := ceph.GetMonitorAddresses(r.Context(), interfaces.CephState{State: s})
	if err != nil {
		return mcTypes.InternalError(err)
	}

	monStatus := types.MonitorStatus{Addresses: monitors}

	return mcTypes.SyncResponse(true, monStatus)

}

func cmdEnableServicePut(s mcTypes.State, r *http.Request) mcTypes.Response {
	var payload types.EnableService

	err := json.NewDecoder(r.Body).Decode(&payload)
	if err != nil {
		logger.Errorf("Failed decoding enable service request: %v", err)
		return mcTypes.InternalError(err)
	}

	err = ceph.ServicePlacementHandler(r.Context(), interfaces.CephState{State: s}, payload)
	if err != nil {
		return mcTypes.SyncResponse(false, err)
	}

	return mcTypes.SyncResponse(true, nil)
}

// Service Reload Endpoint.
var restartServiceCmd = mcTypes.Endpoint{
	Path: "services/restart",
	Post: mcTypes.EndpointAction{Handler: cmdRestartServicePost, ProxyTarget: true},
}

func cmdRestartServicePost(s mcTypes.State, r *http.Request) mcTypes.Response {
	var services types.Services

	err := json.NewDecoder(r.Body).Decode(&services)
	if err != nil {
		logger.Errorf("Failed decoding restart services: %v", err)
		return mcTypes.InternalError(err)
	}

	// Check if provided services are valid and available in microceph
	for _, service := range services {
		valid_services := ceph.GetConfigTableServiceSet()
		if _, ok := valid_services[service.Service]; !ok {
			err := fmt.Errorf("%s is not a valid ceph service", service.Service)
			logger.Errorf("%v", err)
			return mcTypes.InternalError(err)
		}
	}

	clusterServices, err := database.ServiceQuery.List(r.Context(), s)
	if err != nil {
		logger.Errorf("failed fetching services from db: %v", err)
		return mcTypes.SyncResponse(false, err)
	}

	for _, service := range services {
		err = ceph.RestartCephService(clusterServices, service.Service, s.Name())
		if err != nil {
			url := s.Address().String()
			logger.Errorf("Failed restarting %s on host %s", service.Service, url)
			return mcTypes.SyncResponse(false, err)
		}
	}

	return mcTypes.EmptySyncResponse
}

// cmdDeleteService handles service deletion.
func cmdDeleteService(s mcTypes.State, r *http.Request) mcTypes.Response {
	which := path.Base(r.URL.Path)
	_, ok := ceph.GetServicePlacementTable()[which]
	if !ok {
		err := fmt.Errorf("%s is not a valid ceph service", which)
		logger.Errorf("%v", err)
		return mcTypes.InternalError(err)
	}

	err := ceph.DeleteService(r.Context(), interfaces.CephState{State: s}, which)
	if err != nil {
		return mcTypes.SyncResponse(false, err)
	}

	return mcTypes.SyncResponse(true, nil)
}

// cmdNFSDeleteService handles the NFS service deletion.
func cmdNFSDeleteService(s mcTypes.State, r *http.Request) mcTypes.Response {
	var svc types.NFSService

	err := json.NewDecoder(r.Body).Decode(&svc)
	if err != nil {
		logger.Errorf("failed decoding disable service request: %v", err)
		return mcTypes.InternalError(err)
	}

	if !types.NFSClusterIDRegex.MatchString(svc.ClusterID) {
		err := fmt.Errorf("expected cluster_id to be valid (regex: '%s')", types.NFSClusterIDRegex.String())
		return mcTypes.SmartError(err)
	}

	err = ceph.DisableNFS(r.Context(), interfaces.CephState{State: s}, svc.ClusterID)
	if err != nil {
		logger.Errorf("Failed disabling NFS: %v", err)
		return mcTypes.SmartError(err)
	}

	return mcTypes.EmptySyncResponse
}

func cmdRGWServiceDelete(s mcTypes.State, r *http.Request) mcTypes.Response {
	err := ceph.DisableRGW(r.Context(), interfaces.CephState{State: s})
	if err != nil {
		logger.Errorf("Failed disabling RGW: %v", err)
		return mcTypes.SmartError(err)
	}

	return mcTypes.EmptySyncResponse
}
