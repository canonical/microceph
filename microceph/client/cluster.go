package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/api/types"
	microCli "github.com/canonical/microcluster/client"
)

func GetClusterState(ctx context.Context, c *microCli.Client) (string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	var state string

	err := c.Query(queryCtx, "GET", types.ExtendedPathPrefix, api.NewURL().Path("cluster"), nil, &state)
	if err != nil {
		return "", fmt.Errorf("failed to fetch cluster state: %w", err)
	}

	return state, nil
}
