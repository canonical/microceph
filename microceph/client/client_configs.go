package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/clilogger"
	"github.com/canonical/microcluster/v2/client"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/interfaces"
)

func SetClientConfig(ctx context.Context, c *client.Client, data *types.ClientConfig) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*200)
	defer cancel()

	err := c.Query(queryCtx, "PUT", types.ExtendedPathPrefix, api.NewURL().Path("client", "configs", data.Key), data, nil)
	if err != nil {
		return fmt.Errorf("failed setting client config: %w, Key: %s, Value: %s", err, data.Key, data.Value)
	}

	return nil
}

func ResetClientConfig(ctx context.Context, c *client.Client, data *types.ClientConfig) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*200)
	defer cancel()

	err := c.Query(queryCtx, "DELETE", types.ExtendedPathPrefix, api.NewURL().Path("client", "configs", data.Key), data, nil)
	if err != nil {
		return fmt.Errorf("failed clearing client config: %w, Key: %s", err, data.Key)
	}

	return nil
}

func GetClientConfig(ctx context.Context, c *client.Client, data *types.ClientConfig) (types.ClientConfigs, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	configs := types.ClientConfigs{}

	err := c.Query(queryCtx, "GET", types.ExtendedPathPrefix, api.NewURL().Path("client", "configs", data.Key), data, &configs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch client config: %w, Key: %s", err, data.Key)
	}

	return configs, nil
}

func ListClientConfig(ctx context.Context, c *client.Client, data *types.ClientConfig) (types.ClientConfigs, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	configs := types.ClientConfigs{}

	err := c.Query(queryCtx, "GET", types.ExtendedPathPrefix, api.NewURL().Path("client", "configs"), data, &configs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch client config: %w, Key: %s", err, data.Key)
	}

	return configs, nil
}

// /client/configs/
func UpdateClientConf(ctx context.Context, c *client.Client) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*20)
	defer cancel()

	err := c.Query(queryCtx, "PUT", types.ExtendedPathPrefix, api.NewURL().Path("client", "configs"), nil, nil)
	if err != nil {
		return fmt.Errorf("failed to update the configuration file: %w", err)
	}

	return nil
}

// Sends the update conf request to every other member of the cluster.
func SendUpdateClientConfRequestToClusterMembers(ctx context.Context, s interfaces.StateInterface) error {
	// Get a collection of clients to every other cluster member, with the notification user-agent set.
	cluster, err := s.ClusterState().Cluster(false)
	if err != nil {
		clilogger.Errorf("failed to get a client for every cluster member: %v", err)
		return err
	}

	for _, remoteClient := range cluster {
		// In order send restart to each cluster member and wait.
		err = UpdateClientConf(ctx, &remoteClient)
		if err != nil {
			clilogger.Errorf("update conf error: %v", err)
			return err
		}
	}

	return nil
}
