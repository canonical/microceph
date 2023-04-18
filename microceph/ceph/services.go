package ceph

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Rican7/retry"
	"github.com/Rican7/retry/backoff"
	"github.com/Rican7/retry/strategy"
	"github.com/canonical/microcluster/state"
	"github.com/lxc/lxd/shared/logger"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/database"
	"github.com/tidwall/gjson"
)

type Set map[string]struct{}

func (sub Set) isIn(super Set) bool {
	flag := true

	// mark flag false if any key from subset is not present in superset.
	for key := range sub {
		_, ok := super[key]
		if !ok {
			flag = false
			break // Break the loop.
		}
	}

	return flag
}

// Table to map fetchFunc for workers (daemons) to a service.
var serviceWorkerTable = map[string](func () (Set, error)) {
	"osd": getUpOsds,
	"mon": getMons,
}

// Restarts (in order) all Ceph Services provided in the input slice on the host.
func RestartCephServices(services []string) error {
	for i := range services {
		err := RestartCephService(services[i])
		if err != nil {
			logger.Error(fmt.Sprintf("Service %s restart failed: %v ", services[i], err))
			return err
		}
	}

	return nil
}

// Restart provided ceph service ("mon"/"osd"...) on the host.
func RestartCephService(service string) error {
	if _, ok := serviceWorkerTable[service]; !ok {
		errStr := fmt.Sprintf("No handler defined for service %s", service)
		logger.Error(errStr)
		return fmt.Errorf(errStr)
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
		if !workers.isIn(iWorkers) {
			errStr := fmt.Sprintf(
				"Attempt %d: Workers: %v not all present in %v", i, workers, iWorkers,
			)
			logger.Error(errStr)
			return fmt.Errorf(errStr)
		}
		return nil
	}, strategy.Delay(5), strategy.Limit(10), strategy.Backoff(backoff.Linear(10*time.Second)))
	if err != nil {
		return err
	}

	return nil
}

func getMons() (Set, error) {
	retval := Set{}
	output, err := processExec.RunCommand("ceph", "mon", "dump", "-f", "json-pretty")
	if err != nil {
		return nil, err
	}

	// Get a list of mons.
	mons := gjson.Get(output, "mons.#.name")
	for _, key := range mons.Array() {
		retval[key.String()] = struct{}{}
	}

	return retval, nil
}

func getUpOsds() (Set, error) {
	retval := Set{}
	output, err := processExec.RunCommand("ceph", "osd", "dump", "-f", "json-pretty")
	if err != nil {
		return nil, err
	}

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
