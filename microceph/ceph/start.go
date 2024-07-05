package ceph

import (
	"context"
	"database/sql"
	"reflect"
	"time"

	"github.com/canonical/lxd/shared/logger"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

// Start is run on daemon startup.
func Start(s interfaces.StateInterface) error {
	// Start background loop to refresh the config every minute if needed.
	go func() {
		oldMonitors := []string{}

		for {
			// Check that the database is ready.
			err := s.ClusterState().Database.IsOpen(context.Background())
			if err != nil {
				logger.Debug("start: database not ready, waiting...")
				time.Sleep(10 * time.Second)
				continue
			}

			// Get the current list of monitors.
			monitors := []string{}
			err = s.ClusterState().Database.Transaction(s.ClusterState().Context, func(ctx context.Context, tx *sql.Tx) error {
				serviceName := "mon"
				services, err := database.GetServices(ctx, tx, database.ServiceFilter{Service: &serviceName})
				if err != nil {
					return err
				}

				for _, service := range services {
					monitors = append(monitors, service.Member)
				}

				return nil
			})
			if err != nil {
				logger.Warnf("start: failed to fetch monitors, retrying: %v", err)
				time.Sleep(10 * time.Second)
				continue
			}

			// Compare to the previous list.
			if reflect.DeepEqual(oldMonitors, monitors) {
				logger.Debugf("start: monitors unchanged, sleeping: %v", monitors)
				time.Sleep(time.Minute)
				continue
			}

			err = UpdateConfig(s)
			if err != nil {
				logger.Errorf("start: failed to update config, retrying: %v", err)
				time.Sleep(10 * time.Second)
				continue
			}
			logger.Debug("start: updated config, sleeping")
			oldMonitors = monitors
			time.Sleep(time.Minute)
		}
	}()

	return nil
}
