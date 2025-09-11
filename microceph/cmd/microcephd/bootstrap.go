package main

import (
	"context"
	"fmt"
	"os"

	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
	"github.com/pborman/uuid"
)

type Bootstraper interface {
	Prefill(bd common.BootstrapConfig) error
	Precheck(ctx context.Context, state interfaces.StateInterface) error
	Bootstrap(ctx context.Context, state interfaces.StateInterface) error
}

// ##### Skeletons for Bootstraper Implementations #####

// GetBootstraper returns a bootstraper based on the bootstrap parameters.
func GetBootstraper(bd common.BootstrapConfig) Bootstraper {
	sb := SimpleBootstraper{}
	sb.Prefill(bd)

	return &sb
}

// SimpleBootstraper bootstraps microceph with a new ceph cluster.
type SimpleBootstraper struct {
	MonIp      string // IP address of the monitor to be created.
	PublicNet  string // Public Network subnet.
	ClusterNet string // Cluster Network subnet.
	V2Only     bool   // Whether only V2 addresses should be used.
}

// AdoptBootstraper bootstraps microceph with an adopted/existing ceph cluster.
type AdoptBootstraper struct {
	FSID       string   // fsid of the existing ceph cluster.
	MonHosts   []string // slice of exisiting monitor addresses.
	AdminKey   string   // Admin key for providing microceph with privileges.
	PublicNet  string   // Public Network subnet.
	ClusterNet string   // Cluster Network subnet.
}

// ##### Interface Implementations for SimpleBootstraper #####

// Prefill prepares the bootstrap payload sb.
func (sb *SimpleBootstraper) Prefill(bd common.BootstrapConfig) error {
	sb.MonIp = bd.MonIp
	sb.PublicNet = bd.PublicNet
	sb.ClusterNet = bd.ClusterNet
	sb.V2Only = bd.V2Only

	return nil
}

// Precheck verifies all provided values are correct before bootstrapping.
func (sb *SimpleBootstraper) Precheck(ctx context.Context, state interfaces.StateInterface) error {
	var err error

	logger.Debugf("Initiating precheck for simple bootstrap: %v", sb)

	// check network parameters
	err = common.ValidateNetworkParams(state, &sb.MonIp, &sb.PublicNet, &sb.ClusterNet)
	if err != nil {
		logger.Errorf("Network parameter validation failed: %v", err)
		return err
	}

	// check mon v2 parameter
	err = common.ValidateMonV2Param(state, &sb.MonIp, sb.V2Only)
	if err != nil {
		logger.Errorf("Mon v2 parameter validation failed: %v", err)
		return err
	}

	logger.Debugf("Precheck for simple bootstrap successful")

	return nil
}

func (sb *SimpleBootstraper) Bootstrap(ctx context.Context, state interfaces.StateInterface) error {
	fsid := uuid.NewRandom().String()
	pathConsts := constants.GetPathConst()

	logger.Debugf("Bootstrapping new ceph cluster with fsid %s and parameters %v", fsid, sb)

	// Create essential directory paths
	err := ceph.CreateSnapPaths()
	if err != nil {
		return err
	}

	cephConfFile := ceph.CephConfFile{
		FsID:     fsid,
		RunDir:   pathConsts.RunPath,
		Monitors: []string{sb.MonIp},
		PubNet:   sb.PublicNet,
	}

	err = cephConfFile.Render(constants.CephConfFileName)
	if err != nil {
		logger.Errorf("failed to generate ceph.conf: %v", err)
		return err
	}

	path, err := ceph.CreateKeyrings(pathConsts.ConfPath)
	defer os.RemoveAll(path)
	if err != nil {
		return err
	}

	// Update the database as soon as keyrings are available.
	err = ceph.PopulateBootstrapDatabase(ctx, state, fsid, sb.MonIp, sb.PublicNet)
	if err != nil {
		return err
	}

	// Bring up essential ceph services
	err = ceph.BootstrapCephServices(state, path, fsid, sb.MonIp)
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

	// Re-generate the configuration from the database.
	err = ceph.UpdateConfig(ctx, state)
	if err != nil {
		return fmt.Errorf("failed to re-generate the configuration: %w", err)
	}

	logger.Debugf("Successfully bootstrapped new ceph cluster with fsid %s", fsid)

	return nil
}
