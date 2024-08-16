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

// AddDisk requests Ceph sets up a new OSD.
func AddDisk(ctx context.Context, c *microCli.Client, data *types.DisksPost) (types.DiskAddResponse, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	errors := types.DiskAddResponse{}
	err := c.Query(queryCtx, "POST", types.ExtendedPathPrefix, api.NewURL().Path("disks"), data, &errors)
	if err != nil {
		return errors, fmt.Errorf("failed to request disk addition %w", err)
	}

	return errors, nil
}

// GetDisks returns the list of configured disks.
func GetDisks(ctx context.Context, c *microCli.Client) (types.Disks, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	disks := types.Disks{}

	err := c.Query(queryCtx, "GET", types.ExtendedPathPrefix, api.NewURL().Path("disks"), nil, &disks)
	if err != nil {
		return nil, fmt.Errorf("failed listing disks: %w", err)
	}

	return disks, nil
}

// GetResources returns the list of storage devices on the system.
func GetResources(ctx context.Context, c *microCli.Client) (*api.ResourcesStorage, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	storage := api.ResourcesStorage{}

	err := c.Query(queryCtx, "GET", types.ExtendedPathPrefix, api.NewURL().Path("resources"), nil, &storage)
	if err != nil {
		return nil, fmt.Errorf("failed listing storage devices: %w", err)
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
		return fmt.Errorf("failed to get disks: %w", err)
	}
	var location string
	for _, disk := range disks {
		if disk.OSD == data.OSD {
			location = disk.Location
			break
		}
	}
	if location == "" {
		return fmt.Errorf("failed to find location for osd.%d", data.OSD)
	}
	c = c.UseTarget(location)

	err = c.Query(queryCtx, "DELETE", types.ExtendedPathPrefix, api.NewURL().Path("disks", strconv.FormatInt(data.OSD, 10)), data, nil)
	if err != nil {
		// Checking if the error is a context deadline exceeded error
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed to remove disk, timeout (%ds) reached - abort", data.Timeout)
		}
		return fmt.Errorf("failed to remove disk: %w", err)
	}
	return nil
}
