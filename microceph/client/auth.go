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
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
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

// RotateAuthMember asks a single member to rotate and restart only its own daemons:
// deploy the shared mon. keyring (if it runs a mon) and restart it, then stop, rotate,
// and restart its local mgr/osd/mds daemons. Called by the coordinator one member at
// a time so every daemon's keyring is written on the machine that runs it before that
// daemon restarts.
func RotateAuthMember(ctx context.Context, c mcTypes.Client, req *types.MemberAuthRotateRequest) (*types.MemberAuthRotateResponse, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	var resp types.MemberAuthRotateResponse

	err := c.Query(queryCtx, "POST", types.ExtendedPathPrefix, &api.NewURL().Path("auth", "rotate", "member").URL, req, &resp)
	if err != nil {
		return nil, fmt.Errorf("failed member daemon rotation: %w", err)
	}

	return &resp, nil
}

// SendMemberAuthRotateToClusterMembers fans the per-member daemon rotation request
// out to every OTHER cluster member, one member at a time (sequential, mirroring
// SendRestartRequestToClusterMembers), so monitor restarts are interleaved with
// quorum verification. The coordinator handles its own daemons separately.
func SendMemberAuthRotateToClusterMembers(ctx context.Context, s mcTypes.State, req types.MemberAuthRotateRequest) error {
	// Get a collection of clients to every other cluster member, with the notification user-agent set.
	cluster, err := s.Connect().Cluster(false)
	if err != nil {
		return fmt.Errorf("failed to get a client for every cluster member: %w", err)
	}

	for _, remoteClient := range cluster {
		_, err = RotateAuthMember(ctx, remoteClient, &req)
		if err != nil {
			return err
		}
	}

	return nil
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
