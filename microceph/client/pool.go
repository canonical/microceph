// Package client provides a full Go API client.
package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	microCli "github.com/canonical/microcluster/v2/client"

	"github.com/canonical/microceph/microceph/api/types"
)

func PoolSetReplicationFactor(ctx context.Context, c *microCli.Client, data *types.PoolPut) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := c.Query(queryCtx, "PUT", types.ExtendedPathPrefix, api.NewURL().Path("pools-op"), data, nil)
	if err != nil {
		return fmt.Errorf("failed setting replication factor: %w", err)
	}

	return nil
}
