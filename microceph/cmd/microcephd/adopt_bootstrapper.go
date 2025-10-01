package main

import (
	"context"
	"fmt"
	"os"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
)

// AdoptBootstrapper bootstraps microceph with an adopted/existing ceph cluster.
type AdoptBootstrapper struct {
	FSID       string   // fsid of the existing ceph cluster.
	MonHosts   []string // slice of exisiting monitor addresses.
	AdminKey   string   // Admin key for providing microceph with privileges.
	PublicNet  string   // Public Network subnet.
	ClusterNet string   // Cluster Network subnet.
}

// Prefill prepares the bootstrap payload using BootstrapConfig.
func (ab *AdoptBootstrapper) Prefill(bd common.BootstrapConfig, state interfaces.StateInterface) error {
	// populate common parameters
	ab.PublicNet = bd.PublicNet
	ab.ClusterNet = bd.ClusterNet

	// populate adopt specific parameters
	ab.MonHosts = bd.AdoptMonHosts
	ab.AdminKey = bd.AdoptAdminKey
	ab.FSID = bd.AdoptFSID

	// Fetch default network values for microceph services if not provided.
	var monIP string // mon IP unused for adopt bootstrap process.
	err := PopulateDefaultNetworkParams(state, &monIP, &ab.PublicNet, &ab.ClusterNet)
	if err != nil {
		err = fmt.Errorf("failed to populate default network parameters: %v", err)
		logger.Errorf("%v", err)
		return err
	}

	logger.Debugf("Adopt Bootstrap prefill finished with %+v", ab)

	return nil
}

// Precheck verifies all provided values are correct before bootstrapping
func (ab *AdoptBootstrapper) Precheck(ctx context.Context, state interfaces.StateInterface) error {
	if len(ab.FSID) == 0 {
		err := fmt.Errorf("need fsid for adopting a ceph cluster, none provided")
		logger.Error(err.Error())
		return err
	}

	if len(ab.AdminKey) == 0 {
		err := fmt.Errorf("need admin key for adopting a ceph cluster, none provided")
		logger.Error(err.Error())
		return err
	}

	if len(ab.MonHosts) == 0 {
		err := fmt.Errorf("need atleast one mon host for adopting a ceph cluster, none provided")
		logger.Error(err.Error())
		return err
	}

	monIP, _ := common.Network.FindIpOnSubnet(ab.PublicNet)
	err := ValidateNetworkParams(state, &monIP, &ab.PublicNet, &ab.ClusterNet)
	if err != nil {
		return err
	}

	err = ab.generateConfAndKeyring("tmp_ceph.conf", "tmp_ceph.keyring")
	if err != nil {
		err = fmt.Errorf("failed to generate temporary ceph config and keyring files: %v", err)
		logger.Error(err.Error())
		return err
	}

	defer ab.cleanUpTempConfFiles([]string{"tmp_ceph.conf", "tmp_ceph.keyring"})

	err = ceph.VerifyCephClusterConnectivity("tmp_ceph.conf", "tmp_ceph.keyring", ab.MonHosts)
	if err != nil {
		err = fmt.Errorf("failed to connect to the existing ceph cluster: %v", err)
		logger.Error(err.Error())
		return err
	}

	return nil
}

// Bootstrap bootstraps the ceph cluster using the provided parameters.
func (ab *AdoptBootstrapper) Bootstrap(ctx context.Context, state interfaces.StateInterface) error {
	logger.Debugf("Bootstrapping MicroCeph with an existing ceph cluster %s", ab.FSID)

	// Create essential directory paths
	err := ceph.CreateSnapPaths()
	if err != nil {
		logger.Errorf("failed to create essential directory paths: %v", err)
		return err
	}

	err = ab.generateConfAndKeyring(constants.CephConfFileName, constants.CephAdminKeyringFileName)
	if err != nil {
		logger.Errorf("failed to generate ceph.conf and admin keyring file: %v", err)
		return err
	}

	// If a public network is already configured, it will be used instead.
	err = ab.updateCephClusterConfigs()
	if err != nil {
		logger.Errorf("failed to update ceph cluster configurations: %v", err)
		return err
	}

	configs, err := getConfigsforDBUpdation(ab)
	if err != nil {
		return err
	}

	err = ceph.PopulateBootstrapDatabase(ctx, state, []string{}, configs)
	if err != nil {
		return err
	}

	return nil
}

// UpdateCephClusterConfigs configures the ceph cluster network parameters.
func (ab *AdoptBootstrapper) updateCephClusterConfigs() error {
	publicNet, err := ceph.GetConfigItem(types.Config{
		Key: "public_network",
	})
	if err != nil {
		logger.Errorf("failed to get public_network config from ceph cluster: %v", err)
	}

	pn := publicNet[0].Value
	if len(pn) == 0 {
		// Populate MicroCeph deduced public network
		err = ceph.SetConfigItemUnsafe(types.Config{
			Key:   "public_network",
			Value: ab.PublicNet,
		})
		if err != nil {
			logger.Errorf("failed to set public_network config in ceph cluster: %v", err)
			return err
		}
		logger.Debugf("Configured ceph public network as %s", ab.PublicNet)
	}

	clusterNet, err := ceph.GetConfigItem(types.Config{
		Key: "cluster_network",
	})
	if err != nil {
		logger.Errorf("failed to get cluster_network config from ceph cluster: %v", err)
	}

	cn := clusterNet[0].Value
	if len(cn) == 0 {
		// Populate MicroCeph deduced cluster network
		err = ceph.SetConfigItem(types.Config{
			Key:   "cluster_network",
			Value: ab.ClusterNet,
		})
		if err != nil {
			logger.Errorf("failed to set cluster_network config in ceph cluster: %v", err)
			return err
		}
		logger.Debugf("Configured ceph cluster network as %s", ab.ClusterNet)
	}

	return nil
}

var getConfigsforDBUpdation = func(ab *AdoptBootstrapper) (map[string]string, error) {
	configs := map[string]string{
		"fsid":                          ab.FSID,
		constants.AdminKeyringFieldName: ab.AdminKey,
		"public_network":                ab.PublicNet,
	}

	for index, monIP := range ab.MonHosts {
		configs[fmt.Sprintf("mon.host.%d", index+1)] = monIP
	}

	return configs, nil
}

func (ab *AdoptBootstrapper) generateConfAndKeyring(confFileName string, keyringFileName string) error {
	pathConsts := constants.GetPathConst()

	// Create ceph.conf file.
	ccf := ceph.CephConfFile{
		FsID:     ab.FSID,
		RunDir:   pathConsts.RunPath,
		Monitors: ab.MonHosts,
		PubNet:   ab.PublicNet,
	}
	err := ccf.Render(confFileName)
	if err != nil {
		logger.Errorf("failed to render ceph.conf file at %s: %v", confFileName, err)
		return err
	}

	// Create admin keyring file.
	ck := ceph.CephKeyringFile{
		Name: "client.admin",
		Key:  ab.AdminKey,
	}
	err = ck.Render(pathConsts.ConfPath, keyringFileName)
	if err != nil {
		logger.Errorf("failed to render admin keyring file at %s: %v", keyringFileName, err)
		return err
	}

	return nil
}

func (ab *AdoptBootstrapper) cleanUpTempConfFiles(relPaths []string) {
	pathConsts := constants.GetPathConst()

	for _, relPath := range relPaths {
		err := os.Remove(fmt.Sprintf("%s/%s", pathConsts.ConfPath, relPath))
		if err != nil {
			logger.Warnf("failed to remove temporary file %s: %v", relPath, err)
		}
	}
}
