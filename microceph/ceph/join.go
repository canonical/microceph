package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"os"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
)

// Join will join an existing Ceph deployment.
func Join(s interfaces.StateInterface) error {
	pathFileMode := constants.GetPathFileMode()
	var spt = GetServicePlacementTable()

	// Create our various paths.
	for path, perm := range pathFileMode {
		err := os.MkdirAll(path, perm)
		if err != nil {
			return fmt.Errorf("unable to create %q: %w", path, err)
		}
	}

	// Generate the configuration from the database.
	err := UpdateConfig(s)
	if err != nil {
		return fmt.Errorf("failed to generate the configuration: %w", err)
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
	err = updateDatabasePostJoin(s, services)
	if err != nil {
		return fmt.Errorf("failed to update DB post join: %w", err)
	}

	// Start OSD service.
	err = snapStart("osd", true)
	if err != nil {
		return fmt.Errorf("failed to start OSD service: %w", err)
	}

	return nil
}

func updateDatabasePostJoin(s interfaces.StateInterface, services []string) error {
	err := s.ClusterState().Database.Transaction(s.ClusterState().Context, func(ctx context.Context, tx *sql.Tx) error {
		// Record the roles.
		for _, service := range services {
			_, err := database.CreateService(ctx, tx, database.Service{Member: s.ClusterState().Name(), Service: service})
			if err != nil {
				return fmt.Errorf("failed to record role: %w", err)
			}

			if service == "mon" {
				err = updateDbForMon(s, ctx, tx)
				if err != nil {
					return fmt.Errorf("failed to record mon db entries: %w", err)
				}
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func updateDbForMon(s interfaces.StateInterface, ctx context.Context, tx *sql.Tx) error {
	// Fetch public network
	configItem, err := database.GetConfigItem(ctx, tx, "public_network")
	if err != nil {
		return err
	}

	monHost, err := common.Network.FindIpOnSubnet(configItem.Value)
	if err != nil {
		return fmt.Errorf("failed to locate ip on subnet %s: %w", configItem.Value, err)
	}

	key := fmt.Sprintf("mon.host.%s", s.ClusterState().Name())
	_, err = database.CreateConfigItem(ctx, tx, database.ConfigItem{Key: key, Value: monHost})
	if err != nil {
		return fmt.Errorf("failed to record mon host: %w", err)
	}

	return nil
}
