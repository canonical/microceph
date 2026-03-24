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

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/logger"
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

// Testable DB operation wrappers.
var (
	getHostTags       = database.GetHostTags
	joinCreateHostTag = database.CreateHostTag
)

func getAllAZHosts(ctx context.Context, tx *sql.Tx) ([]database.HostTag, error) {
	azKey := "availability-zone"
	tags, err := getHostTags(ctx, tx, database.HostTagFilter{Key: &azKey})
	if err != nil {
		return nil, fmt.Errorf("failed to get host tags: %w", err)
	}

	return tags, nil
}

// Join will join an existing Ceph deployment.
func Join(ctx context.Context, s interfaces.StateInterface, jc common.JoinConfig) error {
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

	// Validate and record the availability zone for this host.
	err = s.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := validateJoinAZ(ctx, tx, jc.AvailabilityZone); err != nil {
			return err
		}
		return setJoinAZ(ctx, tx, s.ClusterState().Name(), jc.AvailabilityZone)
	})
	if err != nil {
		return err
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

	// Hold the lock while starting services to prevent reEnableServices
	// from racing with us on concurrent snapctl calls.
	serviceStartMu.Lock()
	defer serviceStartMu.Unlock()

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

// validateJoinAZ validates availability zone constraints for a joining node.
// The constraint is: no mixed empty and set AZs.
func validateJoinAZ(ctx context.Context, tx *sql.Tx, az string) error {
	if az != "" && !IsValidCrushName(az) {
		return fmt.Errorf("invalid availability zone name %q: must match [a-zA-Z0-9_.-]+", az)
	}

	allAZs, err := getAllAZHosts(ctx, tx)
	if err != nil {
		return err
	}

	const azErrMsg = "mixed empty availability zones and set availability zones are not supported"

	// Empty but others set
	if az == "" && len(allAZs) > 0 {
		return fmt.Errorf(
			"%s: %d existing availability zones found, but join was called without an associated availability zone",
			azErrMsg, len(allAZs),
		)
	}
	// Set, but others empty
	if az != "" && len(allAZs) == 0 {
		return fmt.Errorf(
			"%s: existing hosts do not have an availability zone, but join was called with an associated availability zone",
			azErrMsg,
		)
	}

	return nil
}

// setJoinAZ records the availability zone for a joining node.
// Should be called after validateJoinAZ.
func setJoinAZ(ctx context.Context, tx *sql.Tx, hostname string, az string) error {
	if az == "" {
		return nil
	}

	_, err := joinCreateHostTag(ctx, tx, database.HostTag{Member: hostname, Key: "availability-zone", Value: az})
	if err != nil {
		return fmt.Errorf("failed to record availability zone: %w", err)
	}

	return nil
}

// getServicesForHost get services needed to be spawned on this machine.
var getServicesForHost = func(ctx context.Context, s interfaces.StateInterface, hostname string) ([]database.Service, error) {
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
