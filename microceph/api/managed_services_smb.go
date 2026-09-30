package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

var enableManagedSMBFunc = ceph.EnableManagedSMB
var disableManagedSMBFunc = ceph.DisableManagedSMB

// Each remote placement request is separately bounded. Allow a multi-member
// apply to outlive a single startup wait without running indefinitely.
const managedSMBRequestTimeout = 15 * time.Minute

var managedSMBServiceCmd = mcTypes.Endpoint{
	Path:   "managed-services/smb",
	Put:    mcTypes.EndpointAction{Handler: cmdManagedSMBPut, ProxyTarget: true},
	Delete: mcTypes.EndpointAction{Handler: cmdManagedSMBDelete, ProxyTarget: true},
}

func cmdManagedSMBPut(s mcTypes.State, r *http.Request) mcTypes.Response {
	var request types.ManagedSMBService
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&request)
	var extra any
	if err != nil || decoder.Decode(&extra) != io.EOF {
		// Decoder errors can contain unknown field names or other secret input.
		return mcTypes.BadRequest(fmt.Errorf("invalid managed SMB request JSON"))
	}
	err = validateManagedSMBRequest(request)
	if err != nil {
		return mcTypes.BadRequest(err)
	}

	state := interfaces.CephState{State: s}
	return runManagedSMBRequest(r.Context(), request.ClusterID, request.Wait, func(ctx context.Context) error {
		return enableManagedSMBFunc(ctx, state, request)
	})
}

func cmdManagedSMBDelete(s mcTypes.State, r *http.Request) mcTypes.Response {
	var request types.SMBService
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		logger.Errorf("failed decoding managed SMB disable request: %v", err)
		return mcTypes.InternalError(err)
	}
	if !types.SMBClusterIDRegex.MatchString(request.ClusterID) {
		return mcTypes.SmartError(fmt.Errorf("expected cluster_id to be valid (regex: '%s')", types.SMBClusterIDRegex.String()))
	}

	state := interfaces.CephState{State: s}
	return runManagedSMBRequest(r.Context(), request.ClusterID, true, func(ctx context.Context) error {
		return disableManagedSMBFunc(ctx, state, request.ClusterID)
	})
}

func runManagedSMBRequest(ctx context.Context, clusterID string, wait bool, work func(context.Context) error) mcTypes.Response {
	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), managedSMBRequestTimeout)
	worker := func() error {
		defer cancel()
		return work(operationCtx)
	}
	if wait {
		err := worker()
		if err != nil {
			return mcTypes.SmartError(err)
		}
		return mcTypes.EmptySyncResponse
	}
	go func() {
		err := worker()
		if err != nil {
			logger.Errorf("managed SMB request for %q failed: %v", clusterID, err)
		}
	}()
	// Existing API convention: this is acceptance, not deployment completion.
	return mcTypes.EmptySyncResponse
}

func validateManagedSMBRequest(request types.ManagedSMBService) error {
	if request.Clustering != nil && *request.Clustering != "always" && *request.Clustering != "never" {
		return fmt.Errorf("SMB clustering must be always or never")
	}
	if request.Credentials != nil && len(request.UserGroupRefs) > 0 {
		return fmt.Errorf("provide either credentials or user-group references, not both")
	}
	err := request.Credentials.Validate()
	if err != nil {
		return err
	}
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
		return fmt.Errorf("SMB port must be 0 or between 1 and 65535")
	}
	return nil
}
