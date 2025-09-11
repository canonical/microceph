package main

import (
	"context"

	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microcluster/v2/state"
)

// PreBootstrap is run before the daemon is initialized and bootstrapped.
func PreBootstrap(ctx context.Context, s state.State, initConfig map[string]string) error {
	// Pre Bootstrap's job is to check the source of bootstrap params
	// and verify all expected values are available and correct.

	return nil
}

// PostBootstrap is run after the daemon is initialized and bootstrapped.
func PostBootstrap(ctx context.Context, s state.State, initConfig map[string]string) error {
	data := common.BootstrapConfig{}
	interf := interfaces.CephState{State: s}
	common.DecodeBootstrapConfig(initConfig, &data)
	return ceph.Bootstrap(ctx, interf, data)
}

func PostJoin(ctx context.Context, s state.State, initConfig map[string]string) error {
	interf := interfaces.CephState{State: s}
	return ceph.Join(ctx, interf)
}

func OnStart(ctx context.Context, s state.State) error {
	interf := interfaces.CephState{State: s}
	return ceph.Start(ctx, interf)
}
