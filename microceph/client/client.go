// Package client provides a full Go API client.
package client

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/canonical/lxd/shared/api"
	microCli "github.com/canonical/microcluster/client"

	"github.com/canonical/microceph/microceph/api/types"
)

func SetConfig(ctx context.Context, c *microCli.Client, data *types.Config) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*200)
	defer cancel()

	err := c.Query(queryCtx, "PUT", api.NewURL().Path("configs"), data, nil)
	if err != nil {
		return fmt.Errorf("Failed setting cluster config: %w, Key: %s, Value: %s", err, data.Key, data.Value)
	}

	return nil
}

func ClearConfig(ctx context.Context, c *microCli.Client, data *types.Config) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*200)
	defer cancel()

	err := c.Query(queryCtx, "DELETE", api.NewURL().Path("configs"), data, nil)
	if err != nil {
		return fmt.Errorf("Failed clearing cluster config: %w, Key: %s", err, data.Key)
	}

	return nil
}

func GetConfig(ctx context.Context, c *microCli.Client, data *types.Config) (types.Configs, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	configs := types.Configs{}

	err := c.Query(queryCtx, "GET", api.NewURL().Path("configs"), data, &configs)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch cluster config: %w, Key: %s", err, data.Key)
	}

	return configs, nil
}

// AddDisk requests Ceph sets up a new OSD.
func AddDisk(ctx context.Context, c *microCli.Client, data *types.DisksPost) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := c.Query(queryCtx, "POST", api.NewURL().Path("disks"), data, nil)
	if err != nil {
		return fmt.Errorf("Failed adding new disk: %w", err)
	}

	return nil
}

// GetDisks returns the list of configured disks.
func GetDisks(ctx context.Context, c *microCli.Client) (types.Disks, error) {
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
func GetResources(ctx context.Context, c *microCli.Client) (*api.ResourcesStorage, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	storage := api.ResourcesStorage{}

	err := c.Query(queryCtx, "GET", api.NewURL().Path("resources"), nil, &storage)
	if err != nil {
		return nil, fmt.Errorf("Failed listing storage devices: %w", err)
	}

	return &storage, nil
}

// RemoveDisk requests Ceph removes an OSD.
func RemoveDisk(ctx context.Context, c *microCli.Client, data *types.DisksDelete) error {
	timeout := time.Second * time.Duration(data.Timeout+5) // wait a bit longer than the operation timeout
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// get disks and determine osd location
	disks, err := GetDisks(ctx, c)
	if err != nil {
		return fmt.Errorf("Failed to get disks: %w", err)
	}
	var location string
	for _, disk := range disks {
		if disk.OSD == data.OSD {
			location = disk.Location
			break
		}
	}
	if location == "" {
		return fmt.Errorf("Failed to find location for osd.%d", data.OSD)
	}
	c = c.UseTarget(location)

	err = c.Query(queryCtx, "DELETE", api.NewURL().Path("disks", strconv.FormatInt(data.OSD, 10)), data, nil)
	if err != nil {
		// Checking if the error is a context deadline exceeded error
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("Failed to remove disk, timeout (%ds) reached - abort", data.Timeout)
		}
		return fmt.Errorf("Failed to remove disk: %w", err)
	}
	return nil
}
