// Package client provides a full Go API client.
package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/state"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"

	"github.com/canonical/microceph/microceph/api/types"
)

func SetConfig(ctx context.Context, c *client.Client, data *types.Config) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*200)
	defer cancel()

	err := c.Query(queryCtx, "PUT", api.NewURL().Path("configs"), data, nil)
	if err != nil {
		return fmt.Errorf("Failed setting cluster config: %w, Key: %s, Value: %s", err, data.Key, data.Value)
	}

	return nil
}

func ClearConfig(ctx context.Context, c *client.Client, data *types.Config) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*200)
	defer cancel()

	err := c.Query(queryCtx, "DELETE", api.NewURL().Path("configs"), data, nil)
	if err != nil {
		return fmt.Errorf("Failed clearing cluster config: %w, Key: %s", err, data.Key)
	}

	return nil
}

func GetConfig(ctx context.Context, c *client.Client, data *types.Config) (types.Configs, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	configs := types.Configs{}

	err := c.Query(queryCtx, "GET", api.NewURL().Path("configs"), data, &configs)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch cluster config: %w, Key: %s", err, data.Key)
	}

	return configs, nil
}

func RestartService(ctx context.Context, c *client.Client, data *types.Services) error {
	// 120 second timeout for waiting.
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := c.Query(queryCtx, "POST", api.NewURL().Path("services", "restart"), data, nil)
	if err != nil {
		url := c.URL()
		return fmt.Errorf("Failed Forwarding To: %s: %w", url.String(), err)
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
		logger.Errorf("Failed to get a client for every cluster member: %v", err)
		return err
	}

	for _, remoteClient := range cluster {
		// In order send restart to each cluster member and wait.
		err = RestartService(s.Context, &remoteClient, &data)
		if err != nil {
			logger.Errorf("Restart error: %v", err)
			return err
		}
	}

	return nil
}

// AddDisk requests Ceph sets up a new OSD.
func AddDisk(ctx context.Context, c *client.Client, data *types.DisksPost) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := c.Query(queryCtx, "POST", api.NewURL().Path("disks"), data, nil)
	if err != nil {
		return fmt.Errorf("Failed adding new disk: %w", err)
	}

	return nil
}

// GetDisks returns the list of configured disks.
func GetDisks(ctx context.Context, c *client.Client) (types.Disks, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	disks := types.Disks{}

	err := c.Query(queryCtx, "GET", api.NewURL().Path("disks"), nil, &disks)
	if err != nil {
		return nil, fmt.Errorf("Failed listing disks: %w", err)
	}

	return disks, nil
}

// GetResources returns the list of storage devices on the system.
func GetResources(ctx context.Context, c *client.Client) (*api.ResourcesStorage, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	storage := api.ResourcesStorage{}

	err := c.Query(queryCtx, "GET", api.NewURL().Path("resources"), nil, &storage)
	if err != nil {
		return nil, fmt.Errorf("Failed listing storage devices: %w", err)
	}

	return &storage, nil
}

// GetServices returns the list of configured ceph services.
func GetServices(ctx context.Context, c *client.Client) (types.Services, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	services := types.Services{}

	err := c.Query(queryCtx, "GET", api.NewURL().Path("services"), nil, &services)
	if err != nil {
		return nil, fmt.Errorf("Failed listing services: %w", err)
	}

	return services, nil
}

// EnableRGW requests Ceph configures the RGW service.
func EnableRGW(ctx context.Context, c *client.Client, data *types.RGWService) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()
	err := c.Query(queryCtx, "PUT", api.NewURL().Path("services", "rgw"), data, nil)
	if err != nil {
		return fmt.Errorf("Failed enabling RGW: %w", err)
	}

	return nil
}
