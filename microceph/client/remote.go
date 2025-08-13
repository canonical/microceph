package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/clilogger"
	microCli "github.com/canonical/microcluster/v2/client"
	"github.com/canonical/microcluster/v2/state"
)

// SendRemoteImportRequest sends the remote cluster config key-values for persistence.
func SendRemoteImportRequest(ctx context.Context, c *microCli.Client, data types.RemoteImportRequest) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := c.Query(queryCtx, "PUT", types.ExtendedPathPrefix, api.NewURL().Path("client", "remotes", data.Name), data, nil)
	if err != nil {
		return fmt.Errorf("failed to import MicroCeph remote: %w", err)
	}

	return nil
}

// SendRemoteRemoveRequest sends the remote remove op to MicroCeph.
func SendRemoteRemoveRequest(ctx context.Context, c *microCli.Client, remote string) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*120)
	defer cancel()

	err := c.Query(queryCtx, "DELETE", types.ExtendedPathPrefix, api.NewURL().Path("client", "remotes", remote), nil, nil)
	if err != nil {
		return fmt.Errorf("failed to import MicroCeph remote: %w", err)
	}

	return nil
}

// SendRemoteImportToClusterMembers Sends the remote import request to every other member of the cluster.
func SendRemoteImportToClusterMembers(ctx context.Context, s state.State, data types.RemoteImportRequest) error {
	// Get a collection of clients to every other cluster member.
	cluster, err := s.Cluster(false)
	if err != nil {
		clilogger.Errorf("Remote: failed to get a client for every cluster member: %v", err)
		return err
	}

	clilogger.Infof("Remote: sending info to %d members", len(cluster))

	for _, remoteClient := range cluster {
		// In order send restart to each cluster member and wait.
		err = SendRemoteImportRequest(ctx, &remoteClient, data)
		if err != nil {
			clilogger.Errorf("Remote: error sending to client: %v", err)
			return err
		}
	}

	return nil
}

// FetchAllRemotes queries the remote API and returns a slice of configured remote.
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
