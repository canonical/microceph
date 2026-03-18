package ceph

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/microceph/microceph/logger"
)

// WaitForCephReady polls "ceph -s" until it succeeds, indicating that
// the Ceph can accept commands.
// It retries every second until success or the context is cancelled/expired.
func WaitForCephReady(ctx context.Context) error {
	for {
		_, err := cephRunContext(ctx, "-s")
		if err == nil {
			return nil
		}

		logger.Debugf("Ceph not ready yet: %v", err)

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for Ceph to become ready: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}
