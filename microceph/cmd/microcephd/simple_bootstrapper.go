package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
	"github.com/pborman/uuid"
)

// SimpleBootstrapper bootstraps microceph with a new ceph cluster.
type SimpleBootstrapper struct {
	MonIP      string // IP address of the monitor to be created.
	PublicNet  string // Public Network subnet.
	ClusterNet string // Cluster Network subnet.
	V2Only     bool   // Whether only V2 addresses should be used.
}

// ##### Interface Implementations for SimpleBootstrapper #####

// Prefill prepares the bootstrap payload sb.
func (sb *SimpleBootstrapper) Prefill(bd common.BootstrapConfig, state interfaces.StateInterface) error {
	sb.MonIP = bd.MonIp
	sb.PublicNet = bd.PublicNet
	sb.ClusterNet = bd.ClusterNet
	sb.V2Only = bd.V2Only

	err := PopulateDefaultNetworkParams(state, &sb.MonIP, &sb.PublicNet, &sb.ClusterNet)
	if err != nil {
		logger.Errorf("failed to populate default network parameters: %v", err)
		return err
	}

	PopulateV2OnlyMonIP(&sb.MonIP, sb.V2Only)

	logger.Debugf("Simple Bootstrap prefill finished with %+v", sb)
	return nil
}

// Precheck verifies all provided values are correct before bootstrapping.
func (sb *SimpleBootstrapper) Precheck(ctx context.Context, state interfaces.StateInterface) error {
	var err error

	logger.Debugf("Initiating precheck for simple bootstrap: %+v", sb)

	// drop v2 vectors before validation
	monIP := sb.MonIP
	if sb.V2Only {
		monIP = StripV2OnlyMonIP(monIP)
	}

	// check network parameters
	err = ValidateNetworkParams(state, &monIP, &sb.PublicNet, &sb.ClusterNet)
	if err != nil {
		logger.Errorf("Network parameter validation failed: %v", err)
		return err
	}

	// check mon v2 parameter
	err = ValidateMonV2Param(state, &sb.MonIP, sb.V2Only)
	if err != nil {
		logger.Errorf("Mon v2 parameter validation failed: %v", err)
		return err
	}

	logger.Debugf("Precheck for simple bootstrap successful")

	return nil
}

func (sb *SimpleBootstrapper) Bootstrap(ctx context.Context, state interfaces.StateInterface) error {
	fsid := uuid.NewRandom().String()
	pathConsts := constants.GetPathConst()

	logger.Debugf("Bootstrapping new ceph cluster with fsid %s and parameters %v", fsid, sb)

	// Create essential directory paths
	err := ceph.CreateSnapPaths()
	if err != nil {
		return err
	}

	err = ceph.GenerateCephConfFile(fsid, pathConsts.RunPath, sb.MonIP, sb.PublicNet, constants.CephConfFileName)
	if err != nil {
		err = fmt.Errorf("failed to generate ceph.conf: %w", err)
		logger.Error(err.Error())
		return err
	}

	path, err := ceph.CreateKeyrings(pathConsts.ConfPath)
	defer os.RemoveAll(path)
	if err != nil {
		return err
	}

	services, configs, err := getServicesAndConfigsforDBUpdation(fsid, state.ClusterState().Name(), sb)
	if err != nil {
		return err
	}

	// Update the database as soon as keyrings are available.
	err = ceph.PopulateBootstrapDatabase(ctx, state, services, configs)
	if err != nil {
		return err
	}

	// Bring up essential ceph services
	err = ceph.BootstrapCephServices(state, path, fsid, sb.MonIP)
	if err != nil {
		return err
	}

	// Bring up auto scaling crush rules.
	err = ceph.BootstrapCrushRules()
	if err != nil {
		return err
	}

	// Configure defaults cluster configs for network.
	err = ceph.BootstrapCephConfigs(sb.ClusterNet, sb.PublicNet)
	if err != nil {
		return err
	}

	logger.Debugf("Successfully bootstrapped new ceph cluster with fsid %s", fsid)

	return nil
}

var getServicesAndConfigsforDBUpdation = func(fsid string, hostname string, sb *SimpleBootstrapper) ([]string, map[string]string, error) {
	pathConsts := constants.GetPathConst()
	adminKey, err := ceph.ParseKeyring(filepath.Join(pathConsts.ConfPath, "ceph.client.admin.keyring"))
	if err != nil {
		err = fmt.Errorf("failed parsing admin keyring: %w", err)
		logger.Error(err.Error())
		return nil, nil, err
	}

	services := []string{"mon", "mgr", "mds"}
	configs := map[string]string{
		"fsid":                               fsid,
		constants.AdminKeyringFieldName:      adminKey,
		fmt.Sprintf("mon.host.%s", hostname): sb.MonIP,
		"public_network":                     sb.PublicNet,
	}

	return services, configs, nil
}
