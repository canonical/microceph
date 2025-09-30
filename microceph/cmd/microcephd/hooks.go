package main

import (
	"context"

	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
	"github.com/canonical/microcluster/v2/state"
)

// PreInit is run before the daemon is initialized on bootstrap and join.
func PreInit(ctx context.Context, s state.State, bootstrap bool, initConfig map[string]string) error {
	logger.Infof("Executed Hook: PreInit with %+v", initConfig)
	if bootstrap {
		logger.Infof("PreInit for bootstrap: %v", initConfig)
		// Parse Bootstrap API parameters
		bd := common.BootstrapConfig{}
		common.DecodeBootstrapConfig(initConfig, &bd)

		bootstrapper, err := GetBootstrapper(bd, interfaces.CephState{State: s})
		if err != nil {
			logger.Errorf("failed to get bootstrapper: %v", err)
			return err
		}

		return bootstrapper.Precheck(ctx, interfaces.CephState{State: s})
	}

	return nil
}

// PostBootstrap is run after the daemon is initialized and bootstrapped.
func PostBootstrap(ctx context.Context, s state.State, initConfig map[string]string) error {
	logger.Infof("Executing Hook: PostBootstrap with %+v", initConfig)
	// Parse Bootstrap API parameters
	bd := common.BootstrapConfig{}
	common.DecodeBootstrapConfig(initConfig, &bd)

	bootstrapper, err := GetBootstrapper(bd, interfaces.CephState{State: s})
	if err != nil {
		logger.Errorf("failed to get bootstrapper: %v", err)
		return err
	}

	return bootstrapper.Bootstrap(ctx, interfaces.CephState{State: s})
}

func PostJoin(ctx context.Context, s state.State, initConfig map[string]string) error {
	logger.Infof("Executing Hook: PostJoin with %+v", initConfig)
	interf := interfaces.CephState{State: s}
	return ceph.Join(ctx, interf)
}

func OnStart(ctx context.Context, s state.State) error {
	logger.Info("Executing Hook: OnStart")
	interf := interfaces.CephState{State: s}
	return ceph.Start(ctx, interf)
}
