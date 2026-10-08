package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

var enableManagedSMBFunc = ceph.EnableManagedSMB
var disableManagedSMBFunc = ceph.DisableManagedSMB
var removeManagedSMBServiceFunc = ceph.RemoveManagedSMBService
var deleteManagedSMBClusterFunc = ceph.DeleteManagedSMBCluster

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
		logger.Error("failed decoding managed SMB enable request: invalid JSON")
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
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 2<<20))
	if err != nil {
		logger.Error("failed decoding managed SMB disable request: invalid JSON")
		return mcTypes.BadRequest(fmt.Errorf("invalid managed SMB removal request JSON"))
	}
	var request types.ManagedSMBRemoval
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&request)
	var extra any
	if err != nil || decoder.Decode(&extra) != io.EOF {
		logger.Error("failed decoding managed SMB disable request: invalid JSON")
		return mcTypes.BadRequest(fmt.Errorf("invalid managed SMB removal request JSON"))
	}
	var fields map[string]json.RawMessage
	err = json.Unmarshal(body, &fields)
	if err != nil {
		return mcTypes.BadRequest(fmt.Errorf("managed SMB removal target must not be empty"))
	}
	// Match encoding/json's case-insensitive struct field lookup.
	for field := range fields {
		if strings.EqualFold(field, "target") && request.Target == "" {
			return mcTypes.BadRequest(fmt.Errorf("managed SMB removal target must not be empty"))
		}
	}
	err = validateManagedSMBRemovalRequest(s, r, request)
	if err != nil {
		return mcTypes.BadRequest(err)
	}

	state := interfaces.CephState{State: s}
	return runManagedSMBRequest(r.Context(), request.ClusterID, true, func(ctx context.Context) error {
		switch {
		case request.Target != "":
			return disableManagedSMBFunc(ctx, state, request.ClusterID)
		case request.Force:
			return deleteManagedSMBClusterFunc(ctx, state, request.ClusterID)
		default:
			return removeManagedSMBServiceFunc(ctx, state, request.ClusterID)
		}
	})
}

func validateManagedSMBRemovalRequest(s mcTypes.State, r *http.Request, request types.ManagedSMBRemoval) error {
	err := request.Validate()
	if err != nil {
		return err
	}

	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return fmt.Errorf("invalid managed SMB removal routing query")
	}
	routingTargets, routingTargetWasSet := query["target"]
	if routingTargetWasSet && (len(routingTargets) != 1 || routingTargets[0] == "") {
		return fmt.Errorf("managed SMB removal routing target must not be empty")
	}
	routingTarget := ""
	if routingTargetWasSet {
		routingTarget = routingTargets[0]
		err = (types.ManagedSMBRemoval{ClusterID: request.ClusterID, Target: routingTarget}).Validate()
		if err != nil {
			return err
		}
	}
	if request.Target == "" && routingTarget != "" {
		return fmt.Errorf("managed SMB service and logical-cluster removal must not be routed to a member")
	}
	if request.Target != "" && routingTarget != "" && request.Target != routingTarget {
		return fmt.Errorf("managed SMB removal target conflicts with request routing")
	}
	if request.Target != "" && s.Name() != request.Target {
		return fmt.Errorf("managed SMB removal target does not match the receiving member")
	}
	return nil
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
