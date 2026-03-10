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

// ForceDeleteClusterMember requests MicroCeph to forcibly remove a cluster member while the
// underlying microcluster API is in upgrade waiting state.
func ForceDeleteClusterMember(ctx context.Context, c *microCli.Client, memberName string) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := c.Query(queryCtx, "DELETE", types.ExtendedPathPrefix, api.NewURL().Path("cluster", "members", memberName, "force"), nil, nil)
	if err != nil {
		return fmt.Errorf("failed force-removing cluster member %q: %w", memberName, err)
	}

	return nil
}

// SyncClusterRemotes asks a cluster member to refresh its trust-store records and monitor config
// from the current database state.
func SyncClusterRemotes(ctx context.Context, c *microCli.Client) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := c.Query(queryCtx, "POST", types.ExtendedPathPrefix, api.NewURL().Path("cluster", "remotes", "sync"), nil, nil)
	if err != nil {
		return fmt.Errorf("failed syncing cluster remotes: %w", err)
	}

	return nil
}
