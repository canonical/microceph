// Package client provides a full Go API client.
package client

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/canonical/microcluster/client"
	restTypes "github.com/canonical/microcluster/rest/types"
	"github.com/canonical/microcluster/state"
	"github.com/lxc/lxd/lxd/cluster/request"
	"github.com/lxc/lxd/shared/api"

	"github.com/canonical/microceph/microceph/api/types"
)

func SetConfig(ctx context.Context, c *client.Client, data *types.Config) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	err := c.Query(queryCtx, "PUT", api.NewURL().Path("configs"), data, nil)
	if err != nil {
		return fmt.Errorf("Failed setting cluster config: %w, Key: %s, Value: %s", err, data.Key, data.Value)
	}

	return nil
}

func ClearConfig(ctx context.Context, c *client.Client, data *types.Config) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
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

// IsForwardedRequest determines if this request has been forwarded from another cluster member.
func IsForwardedRequest(r *http.Request) bool {
	return r.Header.Get("User-Agent") == request.UserAgentNotifier
}

func ForwardConfigRequestToClusterMembers(s *state.State, r *http.Request, data *types.Config, handle func(ctx context.Context, c *client.Client, data *types.Config) error) (error) {
	// Get a collection of clients every other cluster member, with the notification user-agent set.
	cluster, err := s.Cluster(r)
	if err != nil {
		return fmt.Errorf("Failed to get a client for every cluster member: %w", err)
	}

	err = cluster.Query(s.Context, true, func(ctx context.Context, c *client.Client) error {
		addrPort, err := restTypes.ParseAddrPort(s.Address().URL.Host)
		if err != nil {
			return fmt.Errorf("Failed to parse addr:port of listen address %q: %w", s.Address().URL.Host, err)
		}

		// Asynchronously send a POST to each other cluster member.
		err = handle(ctx, c, data)
		if err != nil {
			clientURL := c.URL()
			return fmt.Errorf("Failed Forwarding To: %q, From: %s: %w", clientURL.String(), addrPort, err)
		}

		return nil
	})
	if err != nil {
		return err
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
