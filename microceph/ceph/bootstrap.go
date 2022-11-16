// Package ceph has functionality for managing a ceph cluster such as bootstrapping, handling OSDs and status
package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/canonical/microceph/microceph/database"

	"github.com/canonical/microcluster/state"
	"github.com/pborman/uuid"
)

// Bootstrap will initialize a new Ceph deployment.
func Bootstrap(s *state.State) error {
	confPath := filepath.Join(os.Getenv("SNAP_DATA"), "conf")
	runPath := filepath.Join(os.Getenv("SNAP_DATA"), "run")
	dataPath := filepath.Join(os.Getenv("SNAP_COMMON"), "data")
	logPath := filepath.Join(os.Getenv("SNAP_COMMON"), "logs")

	// Create our various paths.
	for _, path := range []string{confPath, runPath, dataPath, logPath} {
		err := os.MkdirAll(path, 0700)
		if err != nil {
			return fmt.Errorf("Unable to create %q: %w", path, err)
		}
	}

	// Generate a new FSID.
	fsid := uuid.NewRandom().String()

	// Generate the initial ceph.conf.
	fd, err := os.OpenFile(filepath.Join(confPath, "ceph.conf"), os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("Couldn't write ceph.conf: %w", err)
	}
	defer fd.Close()

	err = cephConfTpl.Execute(fd, map[string]any{
		"fsid":     fsid,
		"runDir":   runPath,
		"monitors": s.Address().Hostname(),
		"addr":     s.Address().Hostname(),
	})
	if err != nil {
		return fmt.Errorf("Couldn't render ceph.conf: %w", err)
	}

	// Generate the temporary monitor keyring.
	path, err := os.MkdirTemp("", "")
	if err != nil {
		return fmt.Errorf("Unable to create temporary path: %w", err)
	}
	defer os.RemoveAll(path)

	err = genKeyring(filepath.Join(path, "mon.keyring"), "mon.", []string{"mon", "allow *"})
	if err != nil {
		return fmt.Errorf("Failed to generate monitor keyring: %w", err)
	}

	// Generate the admin keyring.
	err = genKeyring(filepath.Join(confPath, "ceph.client.admin.keyring"), "client.admin", []string{"mon", "allow *"}, []string{"osd", "allow *"}, []string{"mds", "allow *"}, []string{"mgr", "allow *"})
	if err != nil {
		return fmt.Errorf("Failed to generate admin keyring: %w", err)
	}

	err = importKeyring(filepath.Join(path, "mon.keyring"), filepath.Join(confPath, "ceph.client.admin.keyring"))
	if err != nil {
		return fmt.Errorf("Failed to generate admin keyring: %w", err)
	}

	adminKey, err := parseKeyring(filepath.Join(confPath, "ceph.client.admin.keyring"))
	if err != nil {
		return fmt.Errorf("Failed parsing admin keyring: %w", err)
	}

	// Generate initial monitor map.
	err = genMonmap(filepath.Join(path, "mon.map"), fsid)
	if err != nil {
		return fmt.Errorf("Failed to generate monitor map: %w", err)
	}

	err = addMonmap(filepath.Join(path, "mon.map"), s.Name(), s.Address().Hostname())
	if err != nil {
		return fmt.Errorf("Failed to generate monitor map: %w", err)
	}

	// Bootstrap the initial monitor.
	monDataPath := filepath.Join(dataPath, "mon", fmt.Sprintf("ceph-%s", s.Name()))

	err = os.MkdirAll(monDataPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap monitor: %w", err)
	}

	err = bootstrapMon(s.Name(), monDataPath, filepath.Join(path, "mon.map"), filepath.Join(path, "mon.keyring"))
	if err != nil {
		return fmt.Errorf("Failed to bootstrap monitor: %w", err)
	}

	err = snapStart("mon", true)
	if err != nil {
		return fmt.Errorf("Failed to start monitor: %w", err)
	}

	// Bootstrap the initial manager.
	mgrDataPath := filepath.Join(dataPath, "mgr", fmt.Sprintf("ceph-%s", s.Name()))

	err = os.MkdirAll(mgrDataPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap manager: %w", err)
	}

	err = bootstrapMgr(s.Name(), mgrDataPath)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap manager: %w", err)
	}

	err = snapStart("mgr", true)
	if err != nil {
		return fmt.Errorf("Failed to start manager: %w", err)
	}

	// Bootstrap the initial metadata server.
	mdsDataPath := filepath.Join(dataPath, "mds", fmt.Sprintf("ceph-%s", s.Name()))

	err = os.MkdirAll(mdsDataPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap metadata server: %w", err)
	}

	err = bootstrapMds(s.Name(), mdsDataPath)
	if err != nil {
		return fmt.Errorf("Failed to bootstrap metadata server: %w", err)
	}

	err = snapStart("mds", true)
	if err != nil {
		return fmt.Errorf("Failed to start metadata server: %w", err)
	}

	// Enable msgr2.
	_, err = cephRun("mon", "enable-msgr2")
	if err != nil {
		return fmt.Errorf("Failed to enable msgr2: %w", err)
	}

	// Start OSD service.
	err = snapStart("osd", true)
	if err != nil {
		return fmt.Errorf("Failed to start OSD service: %w", err)
	}

	// Update the database.
	err = s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		// Record the roles.
		_, err := database.CreateService(ctx, tx, database.Service{Member: s.Name(), Service: "mon"})
		if err != nil {
			return fmt.Errorf("Failed to record role: %w", err)
		}

		_, err = database.CreateService(ctx, tx, database.Service{Member: s.Name(), Service: "mgr"})
		if err != nil {
			return fmt.Errorf("Failed to record role: %w", err)
		}

		_, err = database.CreateService(ctx, tx, database.Service{Member: s.Name(), Service: "mds"})
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
