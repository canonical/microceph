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
