package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/api/types"
)

// RotateAuth triggers or resumes CephX key rotation on the cluster.
func RotateAuth(ctx context.Context, c mcTypes.Client, keyType string, clientName string) (*types.AuthRotateResponse, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	req := types.AuthRotateRequest{
		KeyType: keyType,
		Client:  clientName,
	}
	var resp types.AuthRotateResponse

	err := c.Query(queryCtx, "POST", types.ExtendedPathPrefix, &api.NewURL().Path("auth", "rotate").URL, req, &resp)
	if err != nil {
		return nil, fmt.Errorf("failed to rotate auth keys: %w", err)
	}

	return &resp, nil
}

// GetAuthStatus queries the current CephX key rotation status and client cipher distribution.
func GetAuthStatus(ctx context.Context, c mcTypes.Client) (*types.AuthStatusResponse, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp types.AuthStatusResponse

	err := c.Query(queryCtx, "GET", types.ExtendedPathPrefix, &api.NewURL().Path("auth", "status").URL, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth status: %w", err)
	}

	return &resp, nil
}
