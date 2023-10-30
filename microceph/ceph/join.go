package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
)

// Join will join an existing Ceph deployment.
func Join(s common.StateInterface) error {
	pathFileMode := common.GetPathFileMode()
	var spt = GetServicePlacementTable()

	// Create our various paths.
	for path, perm := range pathFileMode {
		err := os.MkdirAll(path, perm)
		if err != nil {
			return fmt.Errorf("Unable to create %q: %w", path, err)
		}
	}

	// Generate the configuration from the database.
	err := UpdateConfig(s)
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
		err := spt["mon"].ServiceInit(s)
		if err != nil {
			logger.Errorf("%v", err)
			return err
		}

		services = append(services, "mon")
	}

	if srvMgr < 3 {
		err := spt["mgr"].ServiceInit(s)
		if err != nil {
			logger.Errorf("%v", err)
			return err
		}

		services = append(services, "mgr")
	}

	if srvMds < 3 {
		err := spt["mds"].ServiceInit(s)
		if err != nil {
			logger.Errorf("%v", err)
			return err
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
