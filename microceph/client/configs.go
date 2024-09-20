package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/api/types"
	microCli "github.com/canonical/microcluster/v2/client"
)

func SetConfig(ctx context.Context, c *microCli.Client, data *types.Config) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*200)
	defer cancel()

	err := c.Query(queryCtx, "PUT", types.ExtendedPathPrefix, api.NewURL().Path("configs"), data, nil)
	if err != nil {
		return fmt.Errorf("failed setting cluster config: %w, Key: %s, Value: %s", err, data.Key, data.Value)
	}

	return nil
}

func ClearConfig(ctx context.Context, c *microCli.Client, data *types.Config) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*200)
	defer cancel()

	err := c.Query(queryCtx, "DELETE", types.ExtendedPathPrefix, api.NewURL().Path("configs"), data, nil)
	if err != nil {
		return fmt.Errorf("failed clearing cluster config: %w, Key: %s", err, data.Key)
	}

	return nil
}

func GetConfig(ctx context.Context, c *microCli.Client, data *types.Config) (types.Configs, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	configs := types.Configs{}

	err := c.Query(queryCtx, "GET", types.ExtendedPathPrefix, api.NewURL().Path("configs"), data, &configs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch cluster config: %w, Key: %s", err, data.Key)
	}

	return configs, nil
}
