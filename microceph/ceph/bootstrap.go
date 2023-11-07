// Package ceph has functionality for managing a ceph cluster such as bootstrapping, handling OSDs and status
package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pborman/uuid"

	apiTypes "github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
)

// Bootstrap will initialize a new Ceph deployment.
func Bootstrap(s common.StateInterface, data apiTypes.Bootstrap) error {
	pathConsts := common.GetPathConst()
	pathFileMode := common.GetPathFileMode()

	// Create our various paths.
	for path, perm := range pathFileMode {
		err := os.MkdirAll(path, perm)
		if err != nil {
			return fmt.Errorf("unable to create %q: %w", path, err)
		}
	}

	// Generate a new FSID.
	fsid := uuid.NewRandom().String()
	conf := newCephConfig(pathConsts.ConfPath)
	pubNet, err := common.Network.FindNetworkAddress(data.MonIp)
	if err != nil {
		return err
	}

	err = conf.WriteConfig(
		map[string]any{
			"fsid":   fsid,
			"runDir": pathConsts.RunPath,
			// First monitor bootstrap IP as passed to microcluster.
			"monitors": data.MonIp,
			"addr":     data.MonIp,
			"pubnet":   pubNet,
		},
		0644,
	)
	if err != nil {
		return err
	}

	path, err := createKeyrings(pathConsts.ConfPath)
	if err != nil {
		return err
	}

	defer os.RemoveAll(path)

	adminKey, err := parseKeyring(filepath.Join(pathConsts.ConfPath, "ceph.client.admin.keyring"))
	if err != nil {
		return fmt.Errorf("failed parsing admin keyring: %w", err)
	}

	err = createMonMap(s, path, fsid, data.MonIp)
	if err != nil {
		return err
	}

	err = initMon(s, pathConsts.DataPath, path)
	if err != nil {
		return err
	}

	err = initMgr(s, pathConsts.DataPath)
	if err != nil {
		return err
	}

	err = initMds(s, pathConsts.DataPath)
	if err != nil {
		return err
	}

	err = enableMsgr2()
	if err != nil {
		return err
	}

	err = startOSDs(s, pathConsts.DataPath)
	if err != nil {
		return err
	}

	// Update the database.
	err = populateDatabase(s, fsid, adminKey, data)
	if err != nil {
		return err
	}

	// setup up crush rules
	err = ensureCrushRules()
	if err != nil {
		return err
	}
	// configure the default crush rule for new pools
	err = setDefaultCrushRule("microceph_auto_osd")
	if err != nil {
		return err
	}

	// Configure default crush rules.
	err = setDefaultNetworks(pubNet, pubNet)

	// Re-generate the configuration from the database.
	err = UpdateConfig(s)
	if err != nil {
		return fmt.Errorf("failed to re-generate the configuration: %w", err)
	}

	return nil
}

// setDefaultNetworks configures the public and cluster networks on mon KV store.
func setDefaultNetworks(pn string, cn string) error {
	// Public Network
	err := SetConfigItem(apiTypes.Config{
		Key:   "public_network",
		Value: pn,
	})
	if err != nil {
		return err
	}

	// Cluster Network
	err = SetConfigItem(apiTypes.Config{
		Key:   "cluster_network",
		Value: cn,
	})
	if err != nil {
		return err
	}

	return nil
}

func createKeyrings(confPath string) (string, error) {
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

func createMonMap(s common.StateInterface, path string, fsid string, address string) error {
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

func initMon(s common.StateInterface, dataPath string, path string) error {
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
	return nil
}

func initMgr(s common.StateInterface, dataPath string) error {
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

// populateDatabase injects the bootstrap entries to the internal database.
func populateDatabase(s common.StateInterface, fsid string, adminKey string, data apiTypes.Bootstrap) error {
	if s.ClusterState().Database == nil {
		return fmt.Errorf("no database")
	}
	err := s.ClusterState().Database.Transaction(s.ClusterState().Context, func(ctx context.Context, tx *sql.Tx) error {
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

		_, err = database.CreateConfigItem(ctx, tx, database.ConfigItem{Key: "keyring.client.admin", Value: adminKey})
		if err != nil {
			return fmt.Errorf("failed to record keyring: %w", err)
		}

		key := fmt.Sprintf("mon.host.%s", s.ClusterState().Name())
		_, err = database.CreateConfigItem(ctx, tx, database.ConfigItem{Key: key, Value: data.MonIp})
		if err != nil {
			return fmt.Errorf("failed to record mon host: %w", err)
		}

		_, err = database.CreateConfigItem(ctx, tx, database.ConfigItem{Key: "public_network", Value: data.PubNet})
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

func startOSDs(s common.StateInterface, path string) error {
	// Start OSD service.
	err := snapStart("osd", true)
	if err != nil {
		return fmt.Errorf("Failed to start OSD service: %w", err)
	}
	return nil
}

func initMds(s common.StateInterface, dataPath string) error {
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
