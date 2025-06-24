package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

// EnableNFS enables the NFS Ganesha service on the cluster and adds initial configuration.
func EnableNFS(s interfaces.StateInterface, clusterID string, v4MinVersion uint, monitorAddresses []string) error {
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	pathConsts := constants.GetPathConst()
	ganeshaConfDir := filepath.Join(pathConsts.ConfPath, "ganesha")
	err = os.MkdirAll(ganeshaConfDir, 0744)
	if err != nil && !os.IsExist(err) {
		return err
	}

	// Create NFS Ganesha configuration.
	configs := map[string]any{
		"confDir":       ganeshaConfDir,
		"clusterID":     clusterID,
		"minorVersions": v4MinVersion,
	}

	ganeshaConf := newGaneshaConfig(ganeshaConfDir)
	err = ganeshaConf.WriteConfig(configs, 0644)
	if err != nil {
		return err
	}

	// Create NFS Ganesha Ceph configuration.
	configs = map[string]any{
		"confDir":  ganeshaConfDir,
		"monitors": strings.Join(monitorAddresses, ","),
	}

	cephConf := newGaneshaCephConfig(ganeshaConfDir)
	err = cephConf.WriteConfig(configs, 0644)
	if err != nil {
		return err
	}

	// Create NFS Ganesha Ceph keyring.
	err = createNFSKeyring(ganeshaConfDir, clusterID)
	if err != nil {
		return err
	}

	// Create the NFS Pool if needed.
	err = ensureNFSPool(clusterID)
	if err != nil {
		return err
	}

	// Add the node to the Shared Grace Management Database.
	err = addNodeToSharedGraceMgmtDb(filepath.Join(ganeshaConfDir, "ceph.conf"), clusterID, hostname)
	if err != nil {
		return nil
	}

	// Start the NFS Ganesha service.
	return startNFS()
}

// DisableNFS disables the NFS service on the cluster.
func DisableNFS(ctx context.Context, s interfaces.StateInterface) error {
	pathConsts := constants.GetPathConst()
	ganeshaConfDir := filepath.Join(pathConsts.ConfPath, "ganesha")

	// Stop the NFS Ganesha service.
	err := stopNFS()
	if err != nil {
		return err
	}

	// Remove the NFS Ganesha Ceph keyring.
	err = os.Remove(filepath.Join(ganeshaConfDir, "keyring"))
	if err != nil {
		return fmt.Errorf("failed to remove NFS keyring: %w", err)
	}

	// Remove the configuration files.
	err = os.Remove(filepath.Join(ganeshaConfDir, "ceph.conf"))
	if err != nil {
		return fmt.Errorf("failed to remove NFS Ganesha Ceph configuration: %w", err)
	}

	err = os.Remove(filepath.Join(ganeshaConfDir, "ganesha.conf"))
	if err != nil {
		return fmt.Errorf("failed to remove NFS Ganesha configuration: %w", err)
	}

	return nil
}

// nfsCreateServiceDatabase creates a nfs service record in the database.
func nfsCreateServiceDatabase(ctx context.Context, s interfaces.StateInterface) error {
	if s.ClusterState().ServerCert() == nil {
		return fmt.Errorf("no server certificate")
	}

	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Create the service.
		_, err := database.CreateService(ctx, tx, database.Service{Member: s.ClusterState().Name(), Service: "nfs"})
		if err != nil {
			return fmt.Errorf("failed to record role: %w", err)
		}

		return nil
	})
	return err
}

// startNFS starts the NFS service.
func startNFS() error {
	err := snapStart("nfs-ganesha", true)
	if err != nil {
		return fmt.Errorf("failed to start NFS Ganesha service: %w", err)
	}

	return nil
}

// stopNFS stops the NFS service.
func stopNFS() error {
	err := snapStop("nfs-ganesha", true)
	if err != nil {
		return fmt.Errorf("failed to stop NFS Ganesha service: %w", err)
	}

	return nil
}

// createNFSKeyring creates the NFS keyring.
func createNFSKeyring(path, clusterID string) error {
	if err := os.MkdirAll(path, 0770); err != nil {
		return err
	}
	// Create the keyring.
	keyringPath := filepath.Join(path, "keyring")
	if _, err := os.Stat(keyringPath); err == nil {
		return nil
	}

	err := genAuth(
		keyringPath,
		"client.nfs.ganesha",
		[]string{"mon", "allow r"},
		[]string{"osd", fmt.Sprintf("allow rw pool=.nfs namespace=%s", clusterID)})
	if err != nil {
		return err
	}

	return nil
}

// ensureNFSPool creates the NFS Pool for Ganesha if it doesn't exist.
func ensureNFSPool(clusterID string) error {
	_, err := radosCreatePool(".nfs")
	if err != nil && !strings.Contains(err.Error(), "File exists") {
		return fmt.Errorf("failed to create .nfs pool: %w", err)
	}

	// the command is idempotent.
	_, err = osdEnablePool(".nfs", "nfs")
	if err != nil {
		return fmt.Errorf("failed to enable .nfs pool: %w", err)
	}

	_, err = radosCreateObject(".nfs", clusterID, fmt.Sprintf("conf-nfs.%s", clusterID))
	if err != nil && !strings.Contains(err.Error(), "File exists") {
		return fmt.Errorf("failed to create object for Ganesha: %w", err)
	}

	return nil
}

func addNodeToSharedGraceMgmtDb(cephConfPath, clusterID, node string) error {
	// the command is idempotent.
	_, err := ganeshaRadosGrace(cephConfPath, ".nfs", clusterID, "nfs.ganesha", node)
	if err != nil {
		return fmt.Errorf("failed to add node to the shared grace management database: %w", err)
	}

	return nil
}
