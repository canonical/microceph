package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
)

// Join will join an existing Ceph deployment.
func Join(s common.StateInterface) error {
	confPath := filepath.Join(os.Getenv("SNAP_DATA"), "conf")
	runPath := filepath.Join(os.Getenv("SNAP_DATA"), "run")
	dataPath := filepath.Join(os.Getenv("SNAP_COMMON"), "data")
	logPath := filepath.Join(os.Getenv("SNAP_COMMON"), "logs")

	// Create our various paths.
	paths := map[string]os.FileMode{
		confPath: 0755,
		runPath:  0700,
		dataPath: 0700,
		logPath:  0700,
	}

	for path, perm := range paths {
		err := os.MkdirAll(path, perm)
		if err != nil {
			return fmt.Errorf("Unable to create %q: %w", path, err)
		}
	}

	// Generate the configuration from the database.
	err := updateConfig(s)
	if err != nil {
		return fmt.Errorf("Failed to generate the configuration: %w", err)
	}

	// Query existing core services.
	srvMon := 0
	srvMgr := 0
	srvMds := 0

	err = s.ClusterState().Database.Transaction(s.ClusterState().Context, func(ctx context.Context, tx *sql.Tx) error {
		// Monitors.
		name := "mon"
		services, err := database.GetServices(ctx, tx, database.ServiceFilter{Service: &name})
		if err != nil {
			return err
		}

		srvMon = len(services)

		// Managers.
		name = "mgr"
		services, err = database.GetServices(ctx, tx, database.ServiceFilter{Service: &name})
		if err != nil {
			return err
		}

		srvMgr = len(services)

		// Metadata.
		name = "mds"
		services, err = database.GetServices(ctx, tx, database.ServiceFilter{Service: &name})
		if err != nil {
			return err
		}

		srvMds = len(services)

		return nil
	})
	if err != nil {
		return err
	}

	// Add additional services as required.
	services := []string{}

	if srvMon < 3 {
		monDataPath := filepath.Join(dataPath, "mon", fmt.Sprintf("ceph-%s", s.ClusterState().Name()))

		err = os.MkdirAll(monDataPath, 0700)
		if err != nil {
			return fmt.Errorf("Failed to join monitor: %w", err)
		}

		err = joinMon(s.ClusterState().Name(), monDataPath)
		if err != nil {
			return fmt.Errorf("Failed to join monitor: %w", err)
		}

		err = snapStart("mon", true)
		if err != nil {
			return fmt.Errorf("Failed to start monitor: %w", err)
		}

		services = append(services, "mon")
	}

	if srvMgr < 3 {
		mgrDataPath := filepath.Join(dataPath, "mgr", fmt.Sprintf("ceph-%s", s.ClusterState().Name()))

		err = os.MkdirAll(mgrDataPath, 0700)
		if err != nil {
			return fmt.Errorf("Failed to join manager: %w", err)
		}

		err = joinMgr(s.ClusterState().Name(), mgrDataPath)
		if err != nil {
			return fmt.Errorf("Failed to join manager: %w", err)
		}

		err = snapStart("mgr", true)
		if err != nil {
			return fmt.Errorf("Failed to start manager: %w", err)
		}

		services = append(services, "mgr")
	}

	if srvMds < 3 {
		mdsDataPath := filepath.Join(dataPath, "mds", fmt.Sprintf("ceph-%s", s.ClusterState().Name()))

		err = os.MkdirAll(mdsDataPath, 0700)
		if err != nil {
			return fmt.Errorf("Failed to join metadata server: %w", err)
		}

		err = joinMds(s.ClusterState().Name(), mdsDataPath)
		if err != nil {
			return fmt.Errorf("Failed to join metadata server: %w", err)
		}

		err = snapStart("mds", true)
		if err != nil {
			return fmt.Errorf("Failed to start metadata server: %w", err)
		}

		services = append(services, "mds")
	}

	// Update the database.
	err = s.ClusterState().Database.Transaction(s.ClusterState().Context, func(ctx context.Context, tx *sql.Tx) error {
		// Record the roles.
		for _, service := range services {
			_, err := database.CreateService(ctx, tx, database.Service{Member: s.ClusterState().Name(), Service: service})
			if err != nil {
				return fmt.Errorf("Failed to record role: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Start OSD service.
	err = snapStart("osd", true)
	if err != nil {
		return fmt.Errorf("Failed to start OSD service: %w", err)
	}

	return nil
}
