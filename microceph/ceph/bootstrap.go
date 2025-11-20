// Package ceph has functionality for managing a ceph cluster such as bootstrapping, handling OSDs and status
package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"

	apiTypes "github.com/canonical/microceph/microceph/api/types"
)

func CreateSnapPaths() error {
	pathFileMode := constants.GetPathFileMode()

	// Create our various paths.
	for path, perm := range pathFileMode {
		err := os.MkdirAll(path, perm)
		if err != nil {
			return fmt.Errorf("unable to create %q: %w", path, err)
		}
	}

	return nil
}

// BootstrapCephConfigs configures the cluster network on mon KV store.
func BootstrapCephConfigs(cn string, pn string) error {
	// Cluster Network
	err := SetConfigItem(apiTypes.Config{
		Key:   "cluster_network",
		Value: cn,
	})
	if err != nil {
		return err
	}

	// Public Network
	err = SetConfigItemUnsafe(apiTypes.Config{
		Key:   "public_network",
		Value: pn,
	})
	if err != nil {
		return err
	}

	// Default RBD features
	// 63 = layering + exclusive-lock + object-map + fast-diff + deep-flatten + stripingv2
	err = SetConfigItem(apiTypes.Config{
		Key:   "rbd_default_features",
		Value: "63",
	})
	if err != nil {
		return err
	}

	return nil
}

// GenerateCephConfFile generates the ceph.conf file for bootstrap
func GenerateCephConfFile(fsid string, runPath string, monIP string, pubNet string, confFileName string) error {
	var err error
	cephConfFile := CephConfFile{
		FsID:     fsid,
		RunDir:   runPath,
		Monitors: formatIPv6([]string{monIP}),
		PubNet:   pubNet,
	}

	err = cephConfFile.Render(confFileName)
	if err != nil {
		logger.Errorf("failed to generate ceph.conf: %v", err)
		return err
	}

	return nil
}

func CreateKeyrings(confPath string) (string, error) {
	// Generate the temporary monitor keyring.
	path, err := os.MkdirTemp("", "")
	if err != nil {
		return "", fmt.Errorf("Unable to create temporary path: %w", err)
	}

	err = genKeyring(filepath.Join(path, "mon.keyring"), "mon.", []string{"mon", "allow *"})
	if err != nil {
		return "", fmt.Errorf("Failed to generate monitor keyring: %w", err)
	}

	// Generate the admin keyring.
	err = genKeyring(filepath.Join(confPath, "ceph.client.admin.keyring"), "client.admin", []string{"mon", "allow *"}, []string{"osd", "allow *"}, []string{"mds", "allow *"}, []string{"mgr", "allow *"})
	if err != nil {
		return "", fmt.Errorf("Failed to generate admin keyring: %w", err)
	}

	err = importKeyring(filepath.Join(path, "mon.keyring"), filepath.Join(confPath, "ceph.client.admin.keyring"))
	if err != nil {
		return "", fmt.Errorf("Failed to generate admin keyring: %w", err)
	}

	return path, nil
}

// BootstrapCephServices bootstraps the automatic services on simple bootstrap of a ceph cluster.
func BootstrapCephServices(state interfaces.StateInterface, tempKeyringPath string, fsid string, monIP string) error {
	pathConsts := constants.GetPathConst()

	err := createMonMap(state, tempKeyringPath, fsid, monIP)
	if err != nil {
		return err
	}

	err = initMon(state, pathConsts.DataPath, tempKeyringPath)
	if err != nil {
		return err
	}

	err = initMgr(state, pathConsts.DataPath)
	if err != nil {
		return err
	}

	err = initMds(state, pathConsts.DataPath)
	if err != nil {
		return err
	}

	err = enableMsgr2()
	if err != nil {
		return err
	}

	err = initOSDs(state, pathConsts.DataPath)
	if err != nil {
		return err
	}

	return nil
}

// PopulateBootstrapDatabase injects the bootstrap entries to the internal database.
// The function is defined as a var for ease of mocking in tests.
func PopulateBootstrapDatabase(ctx context.Context, s interfaces.StateInterface, services []string, configs map[string]string) error {
	if len(services) == 0 && len(configs) == 0 {
		logger.Debug("No services or configs to populate in the database")
		return nil
	}

	if s.ClusterState().ServerCert() == nil {
		return fmt.Errorf("no server certificate")
	}

	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Record the roles.
		for _, service := range services {
			err := bootstrapDBAddServiceOp(ctx, tx, s.ClusterState().Name(), service)
			if err != nil {
				return err
			}
		}

		// Record the configuration.
		for key, value := range configs {
			err := bootstrapDBAddConfigItemOp(ctx, tx, key, value)
			if err != nil {
				return err
			}
		}

		return nil
	})
	return err
}

// VerifyCephClusterConnectivity verifies if ceph client can talk to a ceph cluster defined by
// 1. A ceph config file located at $confPath and,
// 2. A ceph admin client key located at $keyPath
func VerifyCephClusterConnectivity(confPath string, keyPath string, monitors []string) error {
	pathConsts := constants.GetPathConst()
	cmdArgs := []string{
		"status",
		"--name", "client.admin",
		"-c", fmt.Sprintf("%s/%s", pathConsts.ConfPath, confPath),
		"-k", fmt.Sprintf("%s/%s", pathConsts.ConfPath, keyPath),
		"-m", strings.Join(formatIPv6(monitors), ","),
	}

	_, err := cephRun(cmdArgs...)
	if err != nil {
		err = fmt.Errorf("failed to connect to ceph cluster: %v", err)
		logger.Error(err.Error())
		return err
	}

	return nil
}
