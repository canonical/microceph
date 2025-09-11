package main

import (
	"context"

	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
	"github.com/canonical/microcluster/v2/state"
)

// PreBootstrap is run before the daemon is initialized and bootstrapped.

func PreInit(ctx context.Context, s state.State, bootstrap bool, initConfig map[string]string) error {
	if bootstrap {
		logger.Debugf("PreInit for bootstrap: %v", initConfig)
		// Parse Bootstrap API parameters
		bd := common.BootstrapConfig{}
		common.DecodeBootstrapConfig(initConfig, &bd)

		bootstraper := GetBootstraper(bd)

		return bootstraper.Precheck(ctx, interfaces.CephState{State: s})
	}

	return nil
}

// PostBootstrap is run after the daemon is initialized and bootstrapped.
func PostBootstrap(ctx context.Context, s state.State, initConfig map[string]string) error {
	// Parse Bootstrap API parameters
	bd := common.BootstrapConfig{}
	common.DecodeBootstrapConfig(initConfig, &bd)

	bootstraper := GetBootstraper(bd)

	// paramerter modifications are not carried forward, so we need to precheck again for setting defaults.
	err := bootstraper.Precheck(ctx, interfaces.CephState{State: s})
	if err != nil {
		return nil
	}

	return bootstraper.Bootstrap(ctx, interfaces.CephState{State: s})
}

func PostJoin(ctx context.Context, s state.State, initConfig map[string]string) error {
	interf := interfaces.CephState{State: s}
	return ceph.Join(ctx, interf)
}

func OnStart(ctx context.Context, s state.State) error {
	interf := interfaces.CephState{State: s}
	return ceph.Start(ctx, interf)
}
