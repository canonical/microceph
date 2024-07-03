package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/api/types"
	microCli "github.com/canonical/microcluster/client"
)

// Sends the desired list of services to be restarted on every other member of the cluster.
func SendRemoteImportRequest(ctx context.Context, c *microCli.Client, data types.Remote) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := c.Query(queryCtx, "PUT", types.ExtendedPathPrefix, api.NewURL().Path("client", "remotes", data.Name), data, nil)
	if err != nil {
		return fmt.Errorf("failed to import MicroCeph remote: %w", err)
	}

	return nil
}
