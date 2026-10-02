package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/api/types"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
)

// clusterExportTimeout bounds the cluster export request. The daemon reads the
// cluster config and runs two ceph auth commands, which take several seconds on
// a loaded host, so it matches the 120 s used by the other remote requests.
const clusterExportTimeout = 120 * time.Second

// GetClusterToken fetches the token that lets another cluster import this one as a remote.
func GetClusterToken(ctx context.Context, c mcTypes.Client, req types.ClusterExportRequest) (string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, clusterExportTimeout)
	defer cancel()

	var state string

	err := c.Query(queryCtx, "GET", types.ExtendedPathPrefix, &api.NewURL().Path("cluster").URL, req, &state)
	if err != nil {
		return "", fmt.Errorf("failed to fetch cluster state: %w", err)
	}

	return state, nil
}
