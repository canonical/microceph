package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microcluster/client"
)

func BootstrapCephCluster(ctx context.Context, c *client.Client, data *types.Bootstrap) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*200)
	defer cancel()

	err := c.Query(queryCtx, "PUT", api.NewURL().Path("services"), data, nil)
	if err != nil {
		return fmt.Errorf("failed to bootstrap ceph cluster with parameters %v: %w", data, err)
	}

	return nil
}
