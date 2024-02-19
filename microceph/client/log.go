package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	microCli "github.com/canonical/microcluster/client"

	"github.com/canonical/microceph/microceph/api/types"
)

func LogLevelSet(ctx context.Context, c *microCli.Client, data *types.LogLevelPut) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := c.Query(queryCtx, "PUT", api.NewURL().Path("log-level"), data, nil)
	if err != nil {
		return fmt.Errorf("failed setting log level: %w", err)
	}

	return nil
}

func LogLevelGet(ctx context.Context, c *microCli.Client) (uint32, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	level := uint32(0)

	err := c.Query(queryCtx, "GET", api.NewURL().Path("log-level"), nil, &level)
	if err != nil {
		return 0, fmt.Errorf("failed getting log level: %w", err)
	}

	return level, nil
}
