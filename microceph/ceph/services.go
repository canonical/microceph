package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"os"
	"path/filepath"
	"time"

	"github.com/Rican7/retry"
	"github.com/Rican7/retry/backoff"
	"github.com/Rican7/retry/strategy"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microcluster/state"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/tidwall/gjson"
)

// Table to map fetchFunc for workers (daemons) to a service.
var serviceWorkerTable = map[string](func() (common.Set, error)){
	"osd": getUpOsds,
	"mon": getMons,
}

// Restarts (in order) all Ceph Services provided in the input slice on the host.
func RestartCephServices(services []string) error {
	for i := range services {
		err := RestartCephService(services[i])
		if err != nil {
			logger.Errorf("Service %s restart failed: %v ", services[i], err)
			return err
		}
	}

	return nil
}

// Restart provided ceph service ("mon"/"osd"...) on the host.
func RestartCephService(service string) error {
	if _, ok := serviceWorkerTable[service]; !ok {
		err := fmt.Errorf("No handler defined for service %s", service)
		logger.Errorf("%v", err)
		return err
	}

	// Fetch a Set{} of available daemons for the service.
	workers, err := serviceWorkerTable[service]()
	if err != nil {
		logger.Errorf("Failed fetching service %s workers", service)
		return err
	}

	// Restart the service.
	snapRestart(service, false)

	// Check all the daemons available before Restart are up.
	err = retry.Retry(func(i uint) error {
		iWorkers, err := serviceWorkerTable[service]()
		if err != nil {
			return err
		}

		// All still not up
		if !workers.IsIn(iWorkers) {
			err := fmt.Errorf(
				"Attempt %d: Workers: %v not all present in %v", i, workers, iWorkers,
			)
			logger.Errorf("%v", err)
			return (err)
		}
		return nil
	}, strategy.Delay(5), strategy.Limit(10), strategy.Backoff(backoff.Linear(10*time.Second)))
	if err != nil {
		return err
	}

	return nil
}

func getMons() (common.Set, error) {
	retval := common.Set{}
	output, err := processExec.RunCommand("ceph", "mon", "dump", "-f", "json-pretty")
	if err != nil {
		logger.Errorf("Failed fetching Mon dump: %v", err)
		return nil, err
	}

	logger.Debugf("Mon Dump:\n%s", output)
	// Get a list of mons.
	mons := gjson.Get(output, "mons.#.name")
	for _, key := range mons.Array() {
		retval[key.String()] = struct{}{}
	}

	return retval, nil
}

func getUpOsds() (common.Set, error) {
	retval := common.Set{}
	output, err := processExec.RunCommand("ceph", "osd", "dump", "-f", "json-pretty")
	if err != nil {
		logger.Errorf("Failed fetching OSD dump: %v", err)
		return nil, err
	}

	logger.Debugf("OSD Dump:\n%s", output)
	// Get a list of uuid of osds in up state.
	upOsds := gjson.Get(output, "osds.#(up==1)#.uuid")
	for _, element := range upOsds.Array() {
		retval[element.String()] = struct{}{}
	}
	return retval, nil
}

// ListServices retrieves a list of services from the database
func ListServices(s *state.State) (types.Services, error) {
	services := types.Services{}

	// Get the services from the database.
	err := s.Database.Transaction(s.Context, func(ctx context.Context, tx *sql.Tx) error {
		records, err := database.GetServices(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to fetch service: %w", err)
		}

		for _, service := range records {
			services = append(services, types.Service{
				Location: service.Member,
				Service:  service.Service,
			})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return services, nil
}

// cleanService removes conf data for a service from the cluster.
func cleanService(hostname, service string) error {
	paths := constants.GetPathConst()
	dataPath := filepath.Join(paths.DataPath, service, fmt.Sprintf("ceph-%s", hostname))
	err := os.RemoveAll(dataPath)
	if err != nil {
		logger.Errorf("failed to remove service %q data: %v", service, err)
		return fmt.Errorf("failed to remove service %q data: %w", service, err)
	}
	return nil
}

// removeServiceDatabase removes a service record from the database.
func removeServiceDatabase(s interfaces.StateInterface, service string) error {
	if s.ClusterState().Database == nil {
		return fmt.Errorf("no database")
	}

	err := s.ClusterState().Database.Transaction(s.ClusterState().Context, func(ctx context.Context, tx *sql.Tx) error {
		err := database.DeleteService(ctx, tx, s.ClusterState().Name(), service)
		if err != nil {
			logger.Errorf("failed to remove service from db %q: %v", service, err)
			return fmt.Errorf("failed to remove service from db %q: %w", service, err)
		}

		return nil
	})
	return err
}

// DeleteService deletes a service from the node.
func DeleteService(s interfaces.StateInterface, service string) error {
	err := snapStop(service, true)
	if err != nil {
		logger.Errorf("failed to stop daemon %q: %v", service, err)
		return fmt.Errorf("failed to stop daemon %q: %w", service, err)
	}

	if service == "mon" {
		err = removeMon(s.ClusterState().Name())
		if err != nil {
			return err
		}
	}

	err = cleanService(s.ClusterState().Name(), service)
	if err != nil {
		return fmt.Errorf("failed to clean service %q: %w", service, err)
	}
	err = removeServiceDatabase(s, service)
	if err != nil {
		return fmt.Errorf("failed to remove service %q from database: %w", service, err)
	}
	return nil

}
