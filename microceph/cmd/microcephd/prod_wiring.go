package main

import (
	"context"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// init wires the production implementations of the injectable functions used by
// the placement engine and Ceph-only bootstrap. These functions have default
// stubs that return errors; this init replaces them with real implementations
// that use the microcluster client to interact with cluster members and
// services.
func init() {
	wireProductionFuncs()
}

// wireProductionFuncs injects the production implementations for the placement
// engine and Ceph-only bootstrap functions. It is called from init so that
// daemon API handlers use the real implementations.
func wireProductionFuncs() {
	ceph.GetClusterMemberNamesFunc = prodGetClusterMemberNamesFunc
	ceph.ProdAddControlServiceFunc = prodAddControlServiceFunc
	ceph.ProdRemoveControlServiceFunc = prodRemoveControlServiceFunc
	ceph.BootstrapCephStepsFunc = prodBootstrapCephStepsFunc
}

// prodGetClusterMemberNamesFunc lists MicroCluster member names using the
// cluster leader client.
func prodGetClusterMemberNamesFunc(ctx context.Context, s interfaces.StateInterface) ([]string, error) {
	cli, err := s.ClusterState().Connect().Leader(false)
	if err != nil {
		return nil, fmt.Errorf("failed to get leader client: %w", err)
	}
	members, err := client.MClient.GetClusterMembers(cli)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster members: %w", err)
	}
	return members, nil
}

// prodAddControlServiceFunc enables a control service (mon/mgr/mds) on a target
// member via the service placement API.
func prodAddControlServiceFunc(ctx context.Context, s interfaces.StateInterface, member string, service string) error {
	cli, err := s.ClusterState().Connect().Leader(false)
	if err != nil {
		return fmt.Errorf("failed to get leader client: %w", err)
	}
	return client.SendServicePlacementReq(ctx, cli, &types.EnableService{
		Name: service,
		Wait: true,
	}, member)
}

// prodRemoveControlServiceFunc removes a control service (mon/mgr/mds) from a
// target member via the service deletion API.
func prodRemoveControlServiceFunc(ctx context.Context, s interfaces.StateInterface, member string, service string) error {
	cli, err := s.ClusterState().Connect().Leader(false)
	if err != nil {
		return fmt.Errorf("failed to get leader client: %w", err)
	}
	return client.DeleteService(ctx, cli, member, service)
}

// prodBootstrapCephStepsFunc runs the Ceph bootstrap steps on the local node
// (the handler proxies to the target member via ProxyTarget:true, so the handler
// body executes on the target member where s.Name()==target). It reuses the
// SimpleBootstrapper pipeline: Prefill network params, then Bootstrap to create
// FSID, ceph.conf, keyrings, and initial MON/MGR/MDS services.
func prodBootstrapCephStepsFunc(ctx context.Context, s interfaces.StateInterface, target string, bd common.BootstrapConfig) error {
	logger.Infof("Ceph-only bootstrap: running steps on %s", target)

	// Construct and Prefill a SimpleBootstrapper with the bootstrap config.
	sb := &SimpleBootstrapper{}
	err := sb.Prefill(bd, s)
	if err != nil {
		return fmt.Errorf("failed to prefill simple bootstrapper: %w", err)
	}

	// Run Precheck to validate network parameters.
	err = sb.Precheck(ctx, s)
	if err != nil {
		return fmt.Errorf("precheck failed: %w", err)
	}

	// Run Bootstrap to create FSID, ceph.conf, keyrings, and services.
	err = sb.Bootstrap(ctx, s)
	if err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	return nil
}
