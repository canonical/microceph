package ceph

import (
	"context"
	"database/sql"
	"reflect"
	"time"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
)

// Start is run on daemon startup.
func Start(s common.StateInterface) error {
	// Start background loop to refresh the config every minute if needed.
	go func() {
		oldMonitors := []string{}

		for {
			// Check that the database is ready.
			if !s.ClusterState().Database.IsOpen() {
				time.Sleep(10 * time.Second)
				continue
			}

			// Get the current list of monitors.
			monitors := []string{}
			err := s.ClusterState().Database.Transaction(s.ClusterState().Context, func(ctx context.Context, tx *sql.Tx) error {
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
				time.Sleep(10 * time.Second)
				continue
			}

			// Compare to the previous list.
			if reflect.DeepEqual(oldMonitors, monitors) {
				time.Sleep(time.Minute)
				continue
			}

			err = UpdateConfig(s)
			if err != nil {
				time.Sleep(10 * time.Second)
				continue
			}

			oldMonitors = monitors
			time.Sleep(time.Minute)
		}

	}()

	return nil
}
