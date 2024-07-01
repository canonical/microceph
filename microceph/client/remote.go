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
func SendRemoteImportRequest(ctx context.Context, c *microCli.Client, dict map[string]interface{}) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	// Populate the remote request data.
	var data types.Remote
	for key, value := range dict {
		data.Config[key] = fmt.Sprintf("%s", value)
	}

	err := c.Query(queryCtx, "PUT", types.ExtendedPathPrefix, api.NewURL().Path("remote"), data, nil)
	if err != nil {
		return fmt.Errorf("failed to import MicroCeph remote: %w", err)
	}

	return nil
}
