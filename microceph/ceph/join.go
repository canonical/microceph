package ceph

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
)

func msgrv2OnlyFile(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}

	defer file.Close()
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "mon host") && strings.Contains(line, "v2:") {
			return true, nil
		}
	}

	return false, scanner.Err()
}

func msgrv2OnlyCluster() (bool, error) {
	confPath := filepath.Join(os.Getenv("SNAP_DATA"), "conf", constants.CephConfFileName)
	return msgrv2OnlyFile(confPath)
}

// Join will join an existing Ceph deployment.
func Join(ctx context.Context, s interfaces.StateInterface) error {
	pathFileMode := constants.GetPathFileMode()
	var spt = GetServicePlacementTable()

	// Create our various paths.
	for path, perm := range pathFileMode {
		err := os.MkdirAll(path, perm)
		if err != nil {
			return fmt.Errorf("unable to create %q: %w", path, err)
		}
	}

	// Generate the configuration files from the database.
	err := UpdateConfig(ctx, s)
	if err != nil {
		return fmt.Errorf("failed to generate the configuration: %w", err)
	}

	// check and create service records if needed to be spawned.
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		autoServices := []string{"mon", "mds", "mgr"}
		for _, service := range autoServices {
			err := checkAndCreateServiceRecord(s, ctx, tx, service)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Get services recorded for this host.
	plannedServices, err := getServicesForHost(ctx, s, s.ClusterState().Name())
	if err != nil {
		return err
	}

	// spawn planned auto services.
	for _, service := range plannedServices {
		err := spt[service.Service].ServiceInit(ctx, s)
		if err != nil {
			logger.Errorf("%v", err)
			return err
		}
	}

	// Start OSD service.
	err = snapStart("osd", true)
	if err != nil {
		return fmt.Errorf("failed to start OSD service: %w", err)
	}

	return nil
}

// getServicesForHost get services needed to be spawned on this machine.
func getServicesForHost(ctx context.Context, s interfaces.StateInterface, hostname string) ([]database.Service, error) {
	hostname = s.ClusterState().Name()
	var services []database.Service
	err := s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		services, err = database.GetServices(ctx, tx, database.ServiceFilter{Member: &hostname})
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return services, nil
}

// checkAndCreateServiceRecord check if service is required to be spawned.
func checkAndCreateServiceRecord(s interfaces.StateInterface, ctx context.Context, tx *sql.Tx, name string) error {
	services, err := database.GetServices(ctx, tx, database.ServiceFilter{Service: &name})
	if err != nil {
		return err
	}

	// create record if service is to be spawned.
	if len(services) < 3 {
		_, err := database.CreateService(ctx, tx, database.Service{Member: s.ClusterState().Name(), Service: name})
		if err != nil {
			return fmt.Errorf("failed to record role: %w", err)
		}

		if name == "mon" {
			err = updateDbForMon(s, ctx, tx)
			if err != nil {
				return fmt.Errorf("failed to record mon db entries: %w", err)
			}
		}
	}
	return nil
}

func updateDbForMon(s interfaces.StateInterface, ctx context.Context, tx *sql.Tx) error {
	v2Only, err := msgrv2OnlyCluster()
	if err != nil {
		return err
	}

	// Fetch public network
	configItem, err := database.GetConfigItem(ctx, tx, "public_network")
	if err != nil {
		return err
	}

	monHost, err := common.Network.FindIpOnSubnet(configItem.Value)
	if err != nil {
		return fmt.Errorf("failed to locate ip on subnet %s: %w", configItem.Value, err)
	}

	if v2Only {
		monHost = "v2:" + monHost + ":3300"
	}

	key := fmt.Sprintf("mon.host.%s", s.ClusterState().Name())
	_, err = database.CreateConfigItem(ctx, tx, database.ConfigItem{Key: key, Value: monHost})
	if err != nil {
		return fmt.Errorf("failed to record mon host: %w", err)
	}

	return nil
}
