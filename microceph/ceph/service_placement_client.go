package ceph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/lxd/shared/logger"
)

// Used by client-like services: rbd-mirror
type ClientServicePlacement struct {
	Name string
}

func (gsp *ClientServicePlacement) PopulateParams(s interfaces.StateInterface, payload string) error {
	// No params to initialise.
	return nil
}

func (gsp *ClientServicePlacement) HospitalityCheck(s interfaces.StateInterface) error {
	return genericHospitalityCheck(gsp.Name)
}

func (gsp *ClientServicePlacement) ServiceInit(ctx context.Context, s interfaces.StateInterface) error {
	return clientServiceInit(s, gsp.Name)
}

func (gsp *ClientServicePlacement) PostPlacementCheck(s interfaces.StateInterface) error {
	return genericPostPlacementCheck(gsp.Name)
}

func (gsp *ClientServicePlacement) DbUpdate(ctx context.Context, s interfaces.StateInterface) error {
	return genericDbUpdate(ctx, s, gsp.Name)
}

func clientServiceInit(s interfaces.StateInterface, name string) error {
	var ok bool
	var bootstrapServiceKeyring func(string, string) error
	hostname := s.ClusterState().Name()
	pathConsts := constants.GetPathConst()
	pathFileMode := constants.GetPathFileMode()
	serviceDataPath := filepath.Join(pathConsts.DataPath, name, fmt.Sprintf("ceph-%s", hostname))

	// Fetch addService handler for name service
	bootstrapServiceKeyring, ok = GetServiceKeyringTable()[name]

	if !ok {
		err := fmt.Errorf("%s is not registered in the generic implementation", name)
		logger.Error(err.Error())
		return err
	}

	// Make required directories
	err := os.MkdirAll(serviceDataPath, pathFileMode[pathConsts.DataPath])
	if err != nil {
		logger.Error(err.Error())
		return fmt.Errorf("failed to add datapath %s for service %s: %w", serviceDataPath, name, err)
	}

	err = bootstrapServiceKeyring(hostname, serviceDataPath)
	if err != nil {
		logger.Error(err.Error())
		return fmt.Errorf("failed to add service %s: %w", name, err)
	}

	// create a symlink to conf folder.
	err = createSymlinkToKeyring(
		filepath.Join(serviceDataPath, "keyring"),
		filepath.Join(pathConsts.ConfPath, fmt.Sprintf("ceph.client.%s.%s.keyring", name, hostname)),
	)
	if err != nil {
		return err
	}

	err = snapStart(name, true)
	if err != nil {
		logger.Error(err.Error())
		return fmt.Errorf("failed to perform snap start for service %s: %w", name, err)
	}

	return nil
}

// ================================== HELPERS ==================================

func createSymlinkToKeyring(keyringPath string, confPath string) error {
	err := os.Symlink(keyringPath, confPath)

	if err != nil {
		return fmt.Errorf("failed to create symlink to RGW keyring: %w", err)
	}
	return nil
}
