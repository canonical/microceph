package ceph

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
)

// resolveMonitorAddresses also supports the published Quincy schema, which
// stores monitor placement in services rather than mon.host.* config entries.
func resolveMonitorAddresses(s common.StateInterface, config map[string]string) ([]string, error) {
	addresses := getMonitorAddresses(config)
	if len(addresses) > 0 {
		return addresses, nil
	}
	if s.ClusterState().RemoteAddresses == nil {
		return nil, fmt.Errorf("legacy monitor address lookup is unavailable")
	}

	var monitors []database.Service
	err := s.ClusterState().Database.Transaction(s.ClusterState().Context, func(ctx context.Context, tx *sql.Tx) error {
		service := "mon"
		var err error
		monitors, err = database.GetServices(ctx, tx, database.ServiceFilter{Service: &service})
		return err
	})
	if err != nil {
		return nil, err
	}
	return legacyMonitorAddresses(monitors, s.ClusterState().RemoteAddresses()), nil
}

func legacyMonitorAddresses(monitors []database.Service, remotes map[string]string) []string {
	addresses := make([]string, len(monitors))
	for i, monitor := range monitors {
		addresses[i] = remotes[monitor.Member]
	}
	return addresses
}
