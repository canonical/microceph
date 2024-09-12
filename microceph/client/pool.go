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

func GetPools(ctx context.Context, c *microCli.Client) ([]types.Pool, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	var pools []types.Pool
	err := c.Query(queryCtx, "GET", types.ExtendedPathPrefix, api.NewURL().Path("pools"), nil, &pools)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch OSD pools: %w", err)
	}

	return pools, nil

}
