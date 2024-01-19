// Package client provides a full Go API client.
package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	microCli "github.com/canonical/microcluster/client"

	"github.com/canonical/microceph/microceph/api/types"
)

func PoolSetReplicationFactor(ctx context.Context, c *microCli.Client, data *types.PoolPut) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := c.Query(queryCtx, "POST", api.NewURL().Path("pools"), data, nil)
	if err != nil {
		return fmt.Errorf("failed adding new disk: %w", err)
	}

	return nil
}
