package ceph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

const smbSnapService = "smbd"

// DisableSMB stops a node-local SMB service and removes its local configuration.
func DisableSMB(ctx context.Context, s interfaces.StateInterface, clusterID string) error {
	exists, err := database.GroupedServicesQuery.ExistsOnHost(ctx, s, "smb", clusterID)
	if err != nil {
		return fmt.Errorf("failed to verify SMB service cluster ID: %w", err)
	}
	if !exists {
		return fmt.Errorf("SMB service with ClusterID '%s' not found on node '%s'", clusterID, s.ClusterState().Name())
	}

	err = snapStop(smbSnapService, true)
	if err != nil {
		return fmt.Errorf("failed to stop SMB service: %w", err)
	}

	err = removeSMBLocalState(clusterID)
	if err != nil {
		return err
	}

	err = database.GroupedServicesQuery.RemoveForHost(ctx, s, "smb", clusterID)
	if err != nil {
		return fmt.Errorf("failed to remove SMB service record: %w", err)
	}

	return nil
}

func removeSMBLocalState(clusterID string) error {
	paths := constants.GetPathConst()
	configDir := filepath.Join(paths.ConfPath, "samba")
	runtimeDir := filepath.Join(filepath.Dir(paths.ConfPath), "samba")
	keyringPath := filepath.Join(paths.ConfPath, fmt.Sprintf("ceph.client.smb.fs.cluster.%s.keyring", clusterID))

	err := os.RemoveAll(configDir)
	if err != nil {
		return fmt.Errorf("failed to remove SMB configuration directory: %w", err)
	}

	err = os.RemoveAll(runtimeDir)
	if err != nil {
		return fmt.Errorf("failed to remove SMB runtime directory: %w", err)
	}

	err = os.Remove(keyringPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove SMB CephX keyring: %w", err)
	}

	return nil
}
