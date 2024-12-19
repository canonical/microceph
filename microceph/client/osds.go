// Package client provides a full Go API client.
package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microcluster/v2/client"

	"github.com/canonical/microceph/microceph/api/types"
)

// PutOsds sends the request to '/osds' endpoint to stop or start the OSD service on a target host
func PutOsds(ctx context.Context, c *client.Client, up bool, target string) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	var data types.OsdPut

	switch up {
	case true:
		data = types.OsdPut{State: "up", Location: target}
	case false:
		data = types.OsdPut{State: "down", Location: target}
	}

	c = c.UseTarget(target)
	err := c.Query(queryCtx, "PUT", types.ExtendedPathPrefix, api.NewURL().Path("osds"), data, nil)
	if err != nil {
		url := c.URL()
		logger.Errorf("error changing osd state: %v", err)
		return fmt.Errorf("failed Forwarding To: %s: %w", url.String(), err)
	}
	return nil
}
