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

	clustered := isSMBClusteredLocal()
	services := []string{smbSnapService}
	if clustered {
		services = append(services, "ctdb-nodes", "ctdbd")
	}
	for _, service := range services {
		err = snapStop(service, true)
		if err != nil {
			return fmt.Errorf("failed to stop SMB service %s: %w", service, err)
		}
	}

	err = removeSMBLocalState(clusterID)
	if err != nil {
		return err
	}

	err = database.GroupedServicesQuery.RemoveForHost(ctx, s, "smb", clusterID)
	if err != nil {
		return fmt.Errorf("failed to remove SMB service record: %w", err)
	}
	if clustered {
		err = removeSMBConfigAuthIfUnused(ctx, s, clusterID)
		if err != nil {
			return err
		}
	}

	return nil
}

func removeSMBConfigAuthIfUnused(ctx context.Context, s interfaces.StateInterface, clusterID string) error {
	services, err := database.GroupedServicesQuery.GetGroupedServices(ctx, s)
	if err != nil {
		return fmt.Errorf("failed to check remaining SMB service records: %w", err)
	}
	for _, service := range services {
		if service.Service == "smb" && service.GroupID == clusterID {
			return nil
		}
	}

	entity := fmt.Sprintf("client.smb.config.%s", clusterID)
	_, err = cephRun("auth", "del", entity)
	if err != nil {
		return fmt.Errorf("failed to remove SMB configuration identity: %w", err)
	}
	return nil
}

func isSMBClusteredLocal() bool {
	paths := constants.GetPathConst()
	path := filepath.Join(filepath.Dir(paths.ConfPath), "samba", "ctdb.json")
	_, err := os.Stat(path)
	return err == nil
}

func removeSMBLocalState(clusterID string) error {
	paths := constants.GetPathConst()
	configDir := filepath.Join(paths.ConfPath, "samba")
	runtimeDir := filepath.Join(filepath.Dir(paths.ConfPath), "samba")
	keyringPaths := []string{
		filepath.Join(paths.ConfPath, fmt.Sprintf("ceph.client.smb.fs.cluster.%s.keyring", clusterID)),
		filepath.Join(paths.ConfPath, fmt.Sprintf("ceph.client.smb.config.%s.keyring", clusterID)),
	}

	err := os.RemoveAll(configDir)
	if err != nil {
		return fmt.Errorf("failed to remove SMB configuration directory: %w", err)
	}

	err = os.RemoveAll(runtimeDir)
	if err != nil {
		return fmt.Errorf("failed to remove SMB runtime directory: %w", err)
	}

	if paths.DataPath != "" {
		ctdbIncludePath := filepath.Join(paths.DataPath, "samba", "smb.ctdb.conf")
		err = os.Remove(ctdbIncludePath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove SMB CTDB include: %w", err)
		}
	}

	for _, keyringPath := range keyringPaths {
		err = os.Remove(keyringPath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove SMB CephX keyring: %w", err)
		}
	}

	return nil
}
