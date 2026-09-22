package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
)

var enableManagedSMBFunc = ceph.EnableManagedSMB
var disableManagedSMBFunc = ceph.DisableManagedSMB

var managedSMBServiceCmd = mcTypes.Endpoint{
	Path:   "managed-services/smb",
	Put:    mcTypes.EndpointAction{Handler: cmdManagedSMBPut, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: cmdManagedSMBDelete, ProxyTarget: true},
}

func cmdManagedSMBPut(s mcTypes.State, r *http.Request) mcTypes.Response {
	var request types.ManagedSMBService
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		return mcTypes.InternalError(err)
	}
	err = validateManagedSMBRequest(request)
	if err != nil {
		return mcTypes.SmartError(err)
	}

	state := interfaces.CephState{State: s}
	if request.Wait {
		err = enableManagedSMBFunc(r.Context(), state, request)
		if err != nil {
			return mcTypes.SmartError(err)
		}
		return mcTypes.EmptySyncResponse
	}

	go func() {
		err := enableManagedSMBFunc(context.Background(), state, request)
		if err != nil {
			logger.Errorf("failed enabling managed SMB service: %v", err)
		}
	}()
	return mcTypes.EmptySyncResponse
}

func cmdManagedSMBDelete(s mcTypes.State, r *http.Request) mcTypes.Response {
	var request types.SMBService
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		return mcTypes.InternalError(err)
	}
	if !types.SMBClusterIDRegex.MatchString(request.ClusterID) {
		return mcTypes.SmartError(fmt.Errorf("expected cluster_id to be valid (regex: '%s')", types.SMBClusterIDRegex.String()))
	}

	err = disableManagedSMBFunc(r.Context(), interfaces.CephState{State: s}, request.ClusterID)
	if err != nil {
		return mcTypes.SmartError(err)
	}
	return mcTypes.EmptySyncResponse
}

func validateManagedSMBRequest(request types.ManagedSMBService) error {
	if !types.SMBClusterIDRegex.MatchString(request.ClusterID) {
		return fmt.Errorf("expected cluster_id to be valid (regex: '%s')", types.SMBClusterIDRegex.String())
	}
	if len(request.BindAddresses) > 0 && len(request.BindNetworks) > 0 {
		return fmt.Errorf("SMB bind configuration accepts either addresses or networks, not both")
	}
	for _, address := range request.BindAddresses {
		if net.ParseIP(address) == nil {
			return fmt.Errorf("invalid SMB bind address %q", address)
		}
	}
	for _, network := range request.BindNetworks {
		_, _, err := net.ParseCIDR(network)
		if err != nil {
			return fmt.Errorf("invalid SMB bind network %q", network)
		}
	}
	if request.Port < 0 || request.Port > 65535 {
		return fmt.Errorf("SMB port must be between 1 and 65535")
	}
	return nil
}
