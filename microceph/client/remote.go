package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	microCli "github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/state"
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

// Sends the remote import request to every other member of the cluster.
func SendRemoteImportToClusterMembers(s *state.State, data types.Remote) error {
	// Get a collection of clients to every other cluster member.
	cluster, err := s.Cluster(false)
	if err != nil {
		logger.Errorf("failed to get a client for every cluster member: %v", err)
		return err
	}

	for _, remoteClient := range cluster {
		// In order send restart to each cluster member and wait.
		err = SendRemoteImportRequest(s.Context, &remoteClient, data)
		if err != nil {
			logger.Errorf("restart error: %v", err)
			return err
		}
	}

	return nil
}

// Fetch all remotes
func FetchAllRemotes(ctx context.Context, c *microCli.Client) ([]types.RemoteRecord, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	retval := []types.RemoteRecord{}

	err := c.Query(queryCtx, "GET", types.ExtendedPathPrefix, api.NewURL().Path("client", "remotes"), nil, &retval)
	if err != nil {
		return nil, fmt.Errorf("failed to import MicroCeph remote: %w", err)
	}

	return retval, nil
}
