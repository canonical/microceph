package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microcluster/v2/client"

	"github.com/canonical/microceph/microceph/api/types"
)

// SetRGWCertificate sends a PUT request to set the RGW SSL certificates on the target node.
func SetRGWCertificate(ctx context.Context, c *client.Client, req types.CertificateSetRequest, target string) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	if target != "" {
		c = c.UseTarget(target)
	}

	err := c.Query(queryCtx, "PUT", types.ExtendedPathPrefix, api.NewURL().Path("certificates", "rgw"), req, nil)
	if err != nil {
		return fmt.Errorf("failed to set RGW certificates: %w", err)
	}

	return nil
}
