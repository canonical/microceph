package ceph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

// EnableNFS enables the NFS Ganesha service on the cluster and adds initial configuration.
func EnableNFS(s interfaces.StateInterface, nfs *NFSServicePlacement, monitorAddresses []string) error {
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
	userID := fmt.Sprintf("nfs.%s.%s", nfs.ClusterID, hostname)
	configs := map[string]any{
		"bindAddr":      nfs.BindAddress,
		"bindPort":      nfs.BindPort,
		"confDir":       ganeshaConfDir,
		"userID":        userID,
		"clusterID":     nfs.ClusterID,
		"minorVersions": nfs.V4MinVersion,
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
	err = createNFSKeyring(ganeshaConfDir, nfs.ClusterID, userID)
	if err != nil {
		return err
	}

	// Create the NFS Pools if needed.
	err = ensureNFSPools(nfs.ClusterID)
	if err != nil {
		return err
	}

	// Add the node to the Shared Grace Management Database.
	err = addNodeToSharedGraceMgmtDb(filepath.Join(ganeshaConfDir, "ceph.conf"), nfs.ClusterID, userID, hostname)
	if err != nil {
		return err
	}

	// Start the NFS Ganesha service.
	return startNFS()
}

// DisableNFS disables the NFS service on the cluster.
func DisableNFS(ctx context.Context, s interfaces.StateInterface, clusterID string) error {
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	pathConsts := constants.GetPathConst()
	ganeshaConfDir := filepath.Join(pathConsts.ConfPath, "ganesha")

	// Stop the NFS Ganesha service.
	err = stopNFS()
	if err != nil {
		return err
	}

	// Remove the node from the Shared Grace Management Database.
	userID := fmt.Sprintf("nfs.%s.%s", clusterID, hostname)
	err = removeNodeFromSharedGraceMgmtDb(filepath.Join(ganeshaConfDir, "ceph.conf"), clusterID, userID, hostname)
	if err != nil {
		return err
	}

	// Remove the NFS Ganesha Ceph keyring.
	err = DeleteClientKey(userID)
	if err != nil {
		return fmt.Errorf("failed to remove NFS keyring: %w", err)
	}

	err = os.Remove(filepath.Join(ganeshaConfDir, "keyring"))
	if err != nil {
		return fmt.Errorf("failed to remove NFS keyring file: %w", err)
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

	return database.GroupedServicesQuery.RemoveForHost(ctx, s, "nfs", clusterID)
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
func createNFSKeyring(path, clusterID, userID string) error {
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
		fmt.Sprintf("client.%s", userID),
		[]string{"mon", "allow r"},
		[]string{"osd", fmt.Sprintf("allow rw pool=.nfs namespace=%s", clusterID)})
	if err != nil {
		return err
	}

	return nil
}

// ensureNFSPools creates the NFS data and metadata Pools for Ganesha if they do not exist.
func ensureNFSPools(clusterID string) error {
	_, err := radosRun("ls", "--pool", ".nfs", "--all", "--create")
	if err != nil && !strings.Contains(err.Error(), "File exists") {
		return fmt.Errorf("failed to create .nfs pool: %w", err)
	}

	_, err = radosRun("ls", "--pool", ".nfs.metadata", "--all", "--create")
	if err != nil && !strings.Contains(err.Error(), "File exists") {
		return fmt.Errorf("failed to create .nfs.metadata pool: %w", err)
	}

	// the command is idempotent.
	_, err = osdEnablePoolApp(".nfs", "cephfs")
	if err != nil {
		return fmt.Errorf("failed to enable .nfs pool: %w", err)
	}

	object := fmt.Sprintf("conf-nfs.%s", clusterID)
	_, err = radosRun("create", "--pool", ".nfs", "-N", clusterID, object)
	if err != nil && !strings.Contains(err.Error(), "File exists") {
		return fmt.Errorf("failed to create object for Ganesha: %w", err)
	}

	return nil
}

// osdEnablePoolApp enables the use of an application on the given pool.
func osdEnablePoolApp(pool, app string) (string, error) {
	return cephRun("osd", "pool", "application", "enable", pool, app)
}

// addNodeToSharedGraceMgmtDb adds the given node into the Shared Grace Management Database, which
// is used by the rados_cluster recovery backend.
func addNodeToSharedGraceMgmtDb(cephConfPath, clusterID, userID, node string) error {
	// the command is idempotent.
	_, err := ganeshaRadosGraceRun("--cephconf", cephConfPath, "--pool", ".nfs", "--ns", clusterID, "--userid", userID, "add", node)
	if err != nil {
		return fmt.Errorf("failed to add node to the shared grace management database: %w", err)
	}

	return nil
}

// removeNodeFromSharedGraceMgmtDb removes the given node from the Shared Grace Management Database, which
// is used by the rados_cluster recovery backend.
func removeNodeFromSharedGraceMgmtDb(cephConfPath, clusterID, userID, node string) error {
	// the command is idempotent.
	_, err := ganeshaRadosGraceRun("--cephconf", cephConfPath, "--pool", ".nfs", "--ns", clusterID, "--userid", userID, "remove", node)
	if err != nil {
		return fmt.Errorf("failed to remove node from the shared grace management database: %w", err)
	}

	return nil
}

// ganeshaRadosGraceRun runs the ganesha-rados-grace command with the given arguments.
func ganeshaRadosGraceRun(args ...string) (string, error) {
	return processExec.RunCommand("ganesha-rados-grace", args...)
}
