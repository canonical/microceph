package ceph

import (
	"context"
	"fmt"
	"github.com/canonical/microceph/microceph/common"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

// EnableNFS enables the NFS Ganesha service on the cluster and adds initial configuration.
func EnableNFS(s interfaces.StateInterface, nfs *NFSServicePlacement, monitorAddresses []string) error {
	logger.Debugf("Enabling NFS on node with ClusterID '%s'", nfs.ClusterID)
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

	revert := revert.New()
	defer revert.Fail()
	revert.Add(func() {
		err = os.RemoveAll(ganeshaConfDir)
		if err != nil {
			logger.Errorf("Cleaning up '%s' failed: %v", ganeshaConfDir, err)
		}
	})

	// Create NFS Ganesha configuration.
	userID := fmt.Sprintf("nfs.%s.%s", nfs.ClusterID, hostname)
	configs := map[string]any{
		"bindAddr":      nfs.BindAddress,
		"bindPort":      nfs.BindPort,
		"snapDir":       pathConsts.SnapPath,
		"runDir":        pathConsts.RunPath,
		"confDir":       ganeshaConfDir,
		"userID":        userID,
		"clusterID":     nfs.ClusterID,
		"minorVersions": nfsVersionsStr(nfs.V4MinVersion),
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
	logger.Debugf("Creating ceph client 'client.%s' for NFS Ganesha (ClusterID '%s')", userID, nfs.ClusterID)
	err = createNFSKeyring(ganeshaConfDir, nfs.ClusterID, userID)
	if err != nil {
		return err
	}

	revert.Add(func() {
		err := DeleteClientKey(userID)
		if err != nil {
			logger.Errorf("Cleaning up NFS Ganesha ceph client 'client.%s' failed: %v", userID, err)
		}
	})

	// Create the NFS Pool if needed.
	logger.Debugf("Creating NFS Rados Pool (ClusterID '%s')", nfs.ClusterID)
	err = ensureNFSPool(nfs.ClusterID)
	if err != nil {
		return err
	}

	// Add the node to the Shared Grace Management Database.
	logger.Debugf("Adding node to Shared Grace Management Database (ClusterID '%s')", nfs.ClusterID)
	err = addNodeToSharedGraceMgmtDb(filepath.Join(ganeshaConfDir, "ceph.conf"), nfs.ClusterID, userID, hostname)
	if err != nil {
		return err
	}

	revert.Add(func() {
		err := removeNodeFromSharedGraceMgmtDb(filepath.Join(ganeshaConfDir, "ceph.conf"), nfs.ClusterID, userID, hostname)
		if err != nil {
			logger.Errorf("Removing node from Shared Grace Management Database failed: %v", err)
		}
	})

	// Start the NFS Ganesha service.
	logger.Debugf("Starting NFS Ganesha service (ClusterID '%s')", nfs.ClusterID)
	err = startNFS()
	if err != nil {
		return err
	}

	revert.Success() // Added revert functions are not run on return.

	logger.Debugf("Enabled NFS on node with ClusterID '%s'", nfs.ClusterID)

	return nil
}

func nfsVersionsStr(minVersion uint) string {
	var versions []string

	for i := minVersion; i <= 2; i++ {
		versions = append(versions, strconv.FormatUint(uint64(i), 10))
	}

	return strings.Join(versions, ",")
}

// DisableNFS disables the NFS service on the cluster.
func DisableNFS(ctx context.Context, s interfaces.StateInterface, clusterID string) error {
	exists, err := database.GroupedServicesQuery.ExistsOnHost(ctx, s, "nfs", clusterID)
	if err != nil {
		return fmt.Errorf("failed to verify the node's NFS service ClusterID: %w", err)
	} else if !exists {
		return fmt.Errorf("NFS service with ClusterID '%s' not found on node '%s'", clusterID, s.ClusterState().Name())
	}

	logger.Debugf("Disabling NFS on node with ClusterID '%s'", clusterID)
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	pathConsts := constants.GetPathConst()
	ganeshaConfDir := filepath.Join(pathConsts.ConfPath, "ganesha")

	// Stop the NFS Ganesha service.
	logger.Debugf("Stopping NFS Ganesha service (ClusterID '%s')", clusterID)
	err = stopNFS()
	if err != nil {
		return err
	}

	// Remove the node from the Shared Grace Management Database.
	logger.Debugf("Removing node from Shared Grace Management Database (ClusterID '%s')", clusterID)
	userID := fmt.Sprintf("nfs.%s.%s", clusterID, hostname)
	err = removeNodeFromSharedGraceMgmtDb(filepath.Join(ganeshaConfDir, "ceph.conf"), clusterID, userID, hostname)
	if err != nil {
		return err
	}

	// Remove the NFS Ganesha Ceph keyring.
	logger.Debugf("Removing ceph client 'client.%s' (ClusterID '%s')", userID, clusterID)
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

	// Remove database records.
	logger.Debugf("Removing NFS service records from database (ClusterID '%s')", clusterID)
	err = database.GroupedServicesQuery.RemoveForHost(ctx, s, "nfs", clusterID)
	if err != nil {
		return err
	}

	logger.Debugf("Disabled NFS on node with ClusterID '%s'", clusterID)

	return nil
}

// startNFS starts the NFS service.
func startNFS() error {
	err := snapStart("nfs", true)
	if err != nil {
		return fmt.Errorf("failed to start NFS Ganesha service: %w", err)
	}

	return nil
}

// stopNFS stops the NFS service.
func stopNFS() error {
	err := snapStop("nfs", true)
	if err != nil {
		return fmt.Errorf("failed to stop NFS Ganesha service: %w", err)
	}

	return nil
}

// createNFSKeyring creates the NFS keyring.
func createNFSKeyring(path, clusterID, userID string) error {
	err := os.MkdirAll(path, 0770)
	if err != nil {
		return err
	}
	// Create the keyring.
	keyringPath := filepath.Join(path, "keyring")
	_, err = os.Stat(keyringPath)
	if err == nil {
		return nil
	}

	err = genAuth(
		keyringPath,
		fmt.Sprintf("client.%s", userID),
		[]string{"mon", "allow r"},
		[]string{"osd", fmt.Sprintf("allow rw pool=.nfs namespace=%s", clusterID)})
	if err != nil {
		return err
	}

	return nil
}

// ensureNFSPool creates the NFS Pool for Ganesha if it does not exist.
func ensureNFSPool(clusterID string) error {
	logger.Debugf("Creating '.nfs' rados pool if it doesn't exist.")
	_, err := radosRun("ls", "--pool", ".nfs", "--all", "--create")
	if err != nil && !strings.Contains(err.Error(), "File exists") {
		return fmt.Errorf("failed to create .nfs pool: %w", err)
	}

	// the command is idempotent.
	logger.Debugf("Enabling nfs application on '.nfs' pool.")
	_, err = osdEnablePoolApp(".nfs", "nfs")
	if err != nil {
		return fmt.Errorf("failed to enable 'nfs' on the .nfs pool: %w", err)
	}

	object := fmt.Sprintf("conf-nfs.%s", clusterID)
	logger.Debugf("Creating '%s' rados object in pool '.nfs' in namespace '%s'.", object, clusterID)
	_, err = radosRun("create", "--pool", ".nfs", "-N", clusterID, object)
	if err != nil && !strings.Contains(err.Error(), "File exists") {
		return fmt.Errorf("failed to create object for Ganesha: %w", err)
	}

	logger.Debugf("Finished creating the rados pool and object for NFS with ClusterID %s.", clusterID)
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
	return common.ProcessExec.RunCommand("ganesha-rados-grace", args...)
}
