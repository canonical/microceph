// Package client provides a full Go API client.
package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microcluster/client"
)

func GetS3User(ctx context.Context, c *client.Client, user *types.S3User) (string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*60)
	defer cancel()

	ret := ""
	err := c.Query(queryCtx, "GET", api.NewURL().Path("client", "s3"), user, &ret)
	if err != nil {
		logger.Error(err.Error())
		return ret, err
	}

	return ret, nil
}

func ListS3Users(ctx context.Context, c *client.Client) ([]string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*60)
	defer cancel()

	ret := []string{} // List of usernames
	// GET request with no user name fetches all users.
	err := c.Query(queryCtx, "GET", api.NewURL().Path("client", "s3"), &types.S3User{Name: ""}, &ret)
	if err != nil {
		logger.Error(err.Error())
		return ret, err
	}

	return ret, nil
}

func CreateS3User(ctx context.Context, c *client.Client, user *types.S3User) (string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*60)
	defer cancel()

	ret := ""
	err := c.Query(queryCtx, "PUT", api.NewURL().Path("client", "s3"), user, &ret)
	if err != nil {
		logger.Error(err.Error())
		return ret, err
	}

	return ret, nil
}

func DeleteS3User(ctx context.Context, c *client.Client, user *types.S3User) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*60)
	defer cancel()

	ret := types.S3User{}
	err := c.Query(queryCtx, "DELETE", api.NewURL().Path("client", "s3"), user, &ret)
	if err != nil {
		logger.Error(err.Error())
		return err
	}

	return nil
}
