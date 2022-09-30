// Package client provides a full Go API client.
package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/microcluster/client"
	"github.com/lxc/lxd/shared/api"

	"github.com/canonical/microceph/microceph/api/types"
)

// AddDisk requests Ceph sets up a new OSD.
func AddDisk(ctx context.Context, c *client.Client, data *types.DisksPost) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
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
