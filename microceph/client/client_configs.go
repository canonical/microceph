package client

import (
	"context"
	"fmt"
	"github.com/canonical/microceph/microceph/interfaces"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microcluster/client"
)

func SetClientConfig(ctx context.Context, c *client.Client, data *types.ClientConfig) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*200)
	defer cancel()

	err := c.Query(queryCtx, "PUT", api.NewURL().Path("client", "configs", data.Key), data, nil)
	if err != nil {
		return fmt.Errorf("failed setting client config: %w, Key: %s, Value: %s", err, data.Key, data.Value)
	}

	return nil
}

func ResetClientConfig(ctx context.Context, c *client.Client, data *types.ClientConfig) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*200)
	defer cancel()

	err := c.Query(queryCtx, "DELETE", api.NewURL().Path("client", "configs", data.Key), data, nil)
	if err != nil {
		return fmt.Errorf("failed clearing client config: %w, Key: %s", err, data.Key)
	}

	return nil
}

func GetClientConfig(ctx context.Context, c *client.Client, data *types.ClientConfig) (types.ClientConfigs, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	configs := types.ClientConfigs{}

	err := c.Query(queryCtx, "GET", api.NewURL().Path("client", "configs", data.Key), data, &configs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch client config: %w, Key: %s", err, data.Key)
	}

	return configs, nil
}

func ListClientConfig(ctx context.Context, c *client.Client, data *types.ClientConfig) (types.ClientConfigs, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	configs := types.ClientConfigs{}

	err := c.Query(queryCtx, "GET", api.NewURL().Path("client", "configs"), data, &configs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch client config: %w, Key: %s", err, data.Key)
	}

	return configs, nil
}

// /client/configs/
func UpdateClientConf(ctx context.Context, c *client.Client) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*20)
	defer cancel()

	err := c.Query(queryCtx, "PUT", api.NewURL().Path("client", "configs"), nil, nil)
	if err != nil {
		return fmt.Errorf("failed to update the configuration file: %w", err)
	}

	return nil
}

// Sends the update conf request to every other member of the cluster.
func SendUpdateClientConfRequestToClusterMembers(s interfaces.StateInterface) error {
	// Get a collection of clients to every other cluster member, with the notification user-agent set.
	cluster, err := s.ClusterState().Cluster(nil)
	if err != nil {
		logger.Errorf("failed to get a client for every cluster member: %v", err)
		return err
	}

	for _, remoteClient := range cluster {
		// In order send restart to each cluster member and wait.
		err = UpdateClientConf(s.ClusterState().Context, &remoteClient)
		if err != nil {
			logger.Errorf("update conf error: %v", err)
			return err
		}
	}

	return nil
}
