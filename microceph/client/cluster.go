package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/api/types"
	microCli "github.com/canonical/microcluster/v2/client"
)

func GetClusterToken(ctx context.Context, c *microCli.Client, req types.ClusterExportRequest) (string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	var state string

	err := c.Query(queryCtx, "GET", types.ExtendedPathPrefix, api.NewURL().Path("cluster"), req, &state)
	if err != nil {
		return "", fmt.Errorf("failed to fetch cluster state: %w", err)
	}

	return state, nil
}
