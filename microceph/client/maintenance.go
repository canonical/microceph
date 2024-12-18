// Package client provides a full Go API client.
package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microcluster/v2/client"

	"github.com/canonical/microceph/microceph/api/types"
)

// ExitMaintenance sends the request to '/ops/maintenance/{node}' endpoint to bring a node out of
// maintenance mode.
func ExitMaintenance(ctx context.Context, c *client.Client, node string, dryRun, checkOnly, ignoreCheck bool) (types.MaintenanceResults, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	var results types.MaintenanceResults
	data := types.MaintenancePut{
		Status:           "non-maintenance",
		MaintenanceFlags: types.MaintenanceFlags{DryRun: dryRun, CheckOnly: checkOnly, IgnoreCheck: ignoreCheck},
	}

	// still need to useTarget because some ops need to run on target node
	c = c.UseTarget(node)
	err := c.Query(queryCtx, "PUT", types.ExtendedPathPrefix, api.NewURL().Path("ops", "maintenance", node), data, &results)
	if err != nil {
		logger.Errorf("error bringing node '%s' out of maintenance: %v", node, err)
		return types.MaintenanceResults{}, fmt.Errorf("error bringing node '%s' out of maintenance: %v", node, err)
	}
	return results, nil
}

// EnterMaintenance sends the request to '/ops/maintenance/{node}' endpoint to bring a node into
// maintenance mode.
func EnterMaintenance(ctx context.Context, c *client.Client, node string, force, dryRun, setNoout, stopOsds, checkOnly, ignoreCheck bool) (types.MaintenanceResults, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	var results types.MaintenanceResults
	data := types.MaintenancePut{
		Status:                "maintenance",
		MaintenanceFlags:      types.MaintenanceFlags{DryRun: dryRun, CheckOnly: checkOnly, IgnoreCheck: ignoreCheck},
		MaintenanceEnterFlags: types.MaintenanceEnterFlags{Force: force, SetNoout: setNoout, StopOsds: stopOsds},
	}

	// still need to useTarget because some ops need to run on target node
	c = c.UseTarget(node)
	err := c.Query(queryCtx, "PUT", types.ExtendedPathPrefix, api.NewURL().Path("ops", "maintenance", node), data, &results)
	if err != nil {
		logger.Errorf("error bringing node '%s' into maintenance: %v", node, err)
		return types.MaintenanceResults{}, fmt.Errorf("error bringing node '%s' into maintenance: %v", node, err)
	}
	return results, nil
}
