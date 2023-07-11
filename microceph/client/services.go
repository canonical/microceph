// Package client provides a full Go API client.
package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/state"
)

// GetServices returns the list of configured ceph services.
func GetServices(ctx context.Context, c *client.Client) (types.Services, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	services := types.Services{}

	err := c.Query(queryCtx, "GET", api.NewURL().Path("services"), nil, &services)
	if err != nil {
		return nil, fmt.Errorf("failed listing services: %w", err)
	}

	return services, nil
}

// DisableRGW requests Ceph configures the RGW service.
func DisableRGW(ctx context.Context, c *client.Client, data *types.RGWService) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()
	err := c.Query(queryCtx, "DELETE", api.NewURL().Path("services", "rgw"), data, nil)
	if err != nil {
		return fmt.Errorf("failed disabling RGW: %w", err)
	}

	return nil
}

// Send a request to start certain service at the target node (hostname for remote target).
func SendServicePlacementReq(ctx context.Context, c *client.Client, data *types.EnableService, target string) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	// Send this request to target.
	c = c.UseTarget(target)

	err := c.Query(queryCtx, "PUT", api.NewURL().Path("services", data.Name), data, nil)
	if err != nil {
		return fmt.Errorf("failed placing service %s: %w", data.Name, err)
	}

	return nil
}

// Sends a request to the host to restart the provided service.
func RestartService(ctx context.Context, c *client.Client, data *types.Services) error {
	// 120 second timeout for waiting.
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := c.Query(queryCtx, "POST", api.NewURL().Path("services", "restart"), data, nil)
	if err != nil {
		url := c.URL()
		return fmt.Errorf("failed Forwarding To: %s: %w", url.String(), err)
	}

	return nil
}

// Sends the desired list of services to be restarted on every other member of the cluster.
func SendRestartRequestToClusterMembers(s *state.State, services []string) error {
	// Populate the restart request data.
	var data types.Services
	for _, service := range services {
		data = append(data, types.Service{Service: service})
	}

	// Get a collection of clients to every other cluster member, with the notification user-agent set.
	cluster, err := s.Cluster(nil)
	if err != nil {
		logger.Errorf("failed to get a client for every cluster member: %v", err)
		return err
	}

	for _, remoteClient := range cluster {
		// In order send restart to each cluster member and wait.
		err = RestartService(s.Context, &remoteClient, &data)
		if err != nil {
			logger.Errorf("restart error: %v", err)
			return err
		}
	}

	return nil
}
