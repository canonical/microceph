package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
)

// SetRGWCertificate sends a PUT request to set the RGW SSL certificates on the target node.
// Allow two minutes each for pending recovery, new activation, and verified
// rollback, plus one minute for the surrounding work.
func SetRGWCertificate(ctx context.Context, c mcTypes.Client, req types.CertificateSetRequest, target string) error {
	queryCtx, cancel := context.WithTimeout(ctx, 7*time.Minute)
	defer cancel()

	if target != "" {
		c = c.UseTarget(target)
	}

	err := c.Query(queryCtx, "PUT", types.ExtendedPathPrefix, &api.NewURL().Path("certificates", "rgw").URL, req, nil)
	if err != nil {
		return fmt.Errorf("failed to set RGW certificates: %w", err)
	}

	return nil
}
