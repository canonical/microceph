// Package ceph has functionality for managing a ceph cluster such as bootstrapping, handling OSDs and status
package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"

	apiTypes "github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
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

// waitForMonitor polls 'ceph mon stat' until it succeeds or the timeout is reached.
func waitForMonitor(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		_, err := cephRun("mon", "stat")
		if err == nil {
			logger.Debug("Monitor quorum is active.")
			return nil // Success
		}
		lastErr = err
		logger.Debugf("Waiting for monitor to respond, retrying...")
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for monitor after %v: %w", timeout, lastErr)
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

func BootstrapCephServices(state interfaces.StateInterface, tempKeyringPath string, fsid string, monIp string) error {
	pathConsts := constants.GetPathConst()

	err := createMonMap(state, tempKeyringPath, fsid, monIp)
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

	err = startOSDs(state, pathConsts.DataPath)
	if err != nil {
		return err
	}

	return nil
}

func createMonMap(s interfaces.StateInterface, path string, fsid string, address string) error {
	// Generate initial monitor map.
	err := genMonmap(filepath.Join(path, "mon.map"), fsid)
	if err != nil {
		return fmt.Errorf("failed to generate monitor map: %w", err)
	}

	err = addMonmap(filepath.Join(path, "mon.map"), s.ClusterState().Name(), address)
	if err != nil {
		return fmt.Errorf("failed to add monitor map: %w", err)
	}

	return nil
}

func initMon(s interfaces.StateInterface, dataPath string, path string) error {
	// Bootstrap the initial monitor.
	monDataPath := filepath.Join(dataPath, "mon", fmt.Sprintf("ceph-%s", s.ClusterState().Name()))

	err := os.MkdirAll(monDataPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap monitor: %w", err)
	}

	err = bootstrapMon(s.ClusterState().Name(), monDataPath, filepath.Join(path, "mon.map"), filepath.Join(path, "mon.keyring"))
	if err != nil {
		return fmt.Errorf("Failed to bootstrap monitor: %w", err)
	}

	err = snapStart("mon", true)
	if err != nil {
		return fmt.Errorf("Failed to start monitor: %w", err)
	}

	logger.Debug("Waiting for monitor to become responsive after starting service...")
	err = waitForMonitor(3 * time.Minute)
	if err != nil {
		// Fail bootstrap if the monitor doesn't become responsive.
		return fmt.Errorf("monitor did not become responsive within timeout: %w", err)
	}
	logger.Debug("Monitor is responsive.")

	return nil
}

func initMgr(s interfaces.StateInterface, dataPath string) error {
	// Bootstrap the initial manager.
	mgrDataPath := filepath.Join(dataPath, "mgr", fmt.Sprintf("ceph-%s", s.ClusterState().Name()))

	err := os.MkdirAll(mgrDataPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap manager: %w", err)
	}

	err = bootstrapMgr(s.ClusterState().Name(), mgrDataPath)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap manager: %w", err)
	}

	err = snapStart("mgr", true)
	if err != nil {
		return fmt.Errorf("Failed to start manager: %w", err)
	}
	return nil
}

// PopulateBootstrapDatabase injects the bootstrap entries to the internal database.
func PopulateBootstrapDatabase(ctx context.Context, s interfaces.StateInterface, fsid string, monIp string, pubNet string) error {
	if s.ClusterState().ServerCert() == nil {
		return fmt.Errorf("no server certificate")
	}

	pathConsts := constants.GetPathConst()
	adminKey, err := ParseKeyring(filepath.Join(pathConsts.ConfPath, "ceph.client.admin.keyring"))
	if err != nil {
		return fmt.Errorf("failed parsing admin keyring: %w", err)
	}

	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Record the roles.
		_, err := database.CreateService(ctx, tx, database.Service{Member: s.ClusterState().Name(), Service: "mon"})
		if err != nil {
			return fmt.Errorf("failed to record role: %w", err)
		}

		_, err = database.CreateService(ctx, tx, database.Service{Member: s.ClusterState().Name(), Service: "mgr"})
		if err != nil {
			return fmt.Errorf("failed to record role: %w", err)
		}

		_, err = database.CreateService(ctx, tx, database.Service{Member: s.ClusterState().Name(), Service: "mds"})
		if err != nil {
			return fmt.Errorf("failed to record role: %w", err)
		}

		// Record the configuration.
		_, err = database.CreateConfigItem(ctx, tx, database.ConfigItem{Key: "fsid", Value: fsid})
		if err != nil {
			return fmt.Errorf("failed to record fsid: %w", err)
		}

		_, err = database.CreateConfigItem(ctx, tx, database.ConfigItem{Key: constants.AdminKeyringFieldName, Value: adminKey})
		if err != nil {
			return fmt.Errorf("failed to record keyring: %w", err)
		}

		key := fmt.Sprintf("mon.host.%s", s.ClusterState().Name())
		_, err = database.CreateConfigItem(ctx, tx, database.ConfigItem{Key: key, Value: monIp})
		if err != nil {
			return fmt.Errorf("failed to record mon host: %w", err)
		}

		_, err = database.CreateConfigItem(ctx, tx, database.ConfigItem{Key: "public_network", Value: pubNet})
		if err != nil {
			return fmt.Errorf("failed to record public_network: %w", err)
		}

		return nil
	})
	return err
}

func enableMsgr2() error {
	// Enable msgr2.
	_, err := cephRun("mon", "enable-msgr2")
	if err != nil {
		return fmt.Errorf("Failed to enable msgr2: %w", err)
	}
	return nil
}

func startOSDs(s interfaces.StateInterface, path string) error {
	// Start OSD service.
	err := snapStart("osd", true)
	if err != nil {
		return fmt.Errorf("Failed to start OSD service: %w", err)
	}
	return nil
}

func initMds(s interfaces.StateInterface, dataPath string) error {
	// Bootstrap the initial metadata server.
	mdsDataPath := filepath.Join(dataPath, "mds", fmt.Sprintf("ceph-%s", s.ClusterState().Name()))

	err := os.MkdirAll(mdsDataPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap metadata server: %w", err)
	}

	err = bootstrapMds(s.ClusterState().Name(), mdsDataPath)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap metadata server: %w", err)
	}

	err = snapStart("mds", true)
	if err != nil {
		return fmt.Errorf("Failed to start metadata server: %w", err)
	}
	return nil
}
