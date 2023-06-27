// Package ceph has functionality for managing a ceph cluster such as bootstrapping, handling OSDs and status
package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pborman/uuid"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
)

// Bootstrap will initialize a new Ceph deployment.
func Bootstrap(s common.StateInterface) error {
	pathConsts := common.GetPathConst()
	pathFileMode := common.GetPathFileMode()

	// Create our various paths.
	for path, perm := range pathFileMode {
		err := os.MkdirAll(path, perm)
		if err != nil {
			return fmt.Errorf("Unable to create %q: %w", path, err)
		}
	}

	// Generate a new FSID.
	fsid := uuid.NewRandom().String()
	conf := newCephConfig(pathConsts.ConfPath)
	err := conf.WriteConfig(
		map[string]any{
			"fsid":     fsid,
			"runDir":   pathConsts.RunPath,
			"monitors": s.ClusterState().Address().Hostname(),
			"addr":     s.ClusterState().Address().Hostname(),
		},
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
		return fmt.Errorf("Failed parsing admin keyring: %w", err)
	}

	err = createMonMap(s, path, fsid)
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
	err = updateDatabase(s, fsid, adminKey)
	if err != nil {
		return err
	}

	// ensure crush rules
	err = ensureCrushRules()
	if err != nil {
		return err
	}

	// Re-generate the configuration from the database.
	err = updateConfig(s)
	if err != nil {
		return fmt.Errorf("Failed to re-generate the configuration: %w", err)
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

func createMonMap(s common.StateInterface, path string, fsid string) error {
	// Generate initial monitor map.
	err := genMonmap(filepath.Join(path, "mon.map"), fsid)
	if err != nil {
		return fmt.Errorf("Failed to generate monitor map: %w", err)
	}

	err = addMonmap(filepath.Join(path, "mon.map"), s.ClusterState().Name(), s.ClusterState().Address().Hostname())
	if err != nil {
		return fmt.Errorf("Failed to add monitor map: %w", err)
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

func updateDatabase(s common.StateInterface, fsid string, adminKey string) error {
	if s.ClusterState().Database == nil {
		return fmt.Errorf("no database")
	}
	err := s.ClusterState().Database.Transaction(s.ClusterState().Context, func(ctx context.Context, tx *sql.Tx) error {
		// Record the roles.
		_, err := database.CreateService(ctx, tx, database.Service{Member: s.ClusterState().Name(), Service: "mon"})
		if err != nil {
			return fmt.Errorf("Failed to record role: %w", err)
		}

		_, err = database.CreateService(ctx, tx, database.Service{Member: s.ClusterState().Name(), Service: "mgr"})
		if err != nil {
			return fmt.Errorf("Failed to record role: %w", err)
		}

		_, err = database.CreateService(ctx, tx, database.Service{Member: s.ClusterState().Name(), Service: "mds"})
		if err != nil {
			return fmt.Errorf("Failed to record role: %w", err)
		}

		// Record the configuration.
		_, err = database.CreateConfigItem(ctx, tx, database.ConfigItem{Key: "fsid", Value: fsid})
		if err != nil {
			return fmt.Errorf("Failed to record fsid: %w", err)
		}

		_, err = database.CreateConfigItem(ctx, tx, database.ConfigItem{Key: "keyring.client.admin", Value: adminKey})
		if err != nil {
			return fmt.Errorf("Failed to record keyring: %w", err)
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

// ensureCrushRules removes the default replicated rule and adds a microceph default rule with failure domain OSD
func ensureCrushRules() error {
	// Remove the default replicated rule it it exists.
	if haveCrushRule("replicated_rule") {
		err := removeCrushRule("replicated_rule")
		if err != nil {
			return fmt.Errorf("Failed to remove default replicated rule: %w", err)
		}
	}
	// Add a microceph default rule with failure domain OSD if it does not exist.
	if haveCrushRule("microceph_auto_rule") {
		return nil
	}
	err := addCrushRule("microceph_auto_osd", "osd")
	if err != nil {
		return fmt.Errorf("Failed to add microceph default rule: %w", err)
	}
	return nil
}
