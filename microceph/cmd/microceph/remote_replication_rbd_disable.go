package main

import (
	"context"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdRemoteReplicationDisableRbd struct {
	common  *CmdControl
	isForce bool
}

func (c *cmdRemoteReplicationDisableRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable <resource>",
		Short: "Disable remote replication for RBD resource (Pool or Image)",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.isForce, "force", false, "forcefully disable replication for rbd resource")
	return cmd
}

func (c *cmdRemoteReplicationDisableRbd) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	payload, err := c.prepareRbdPayload(types.DisableReplicationRequest, args)
	if err != nil {
		return err
	}

	_, err = client.SendRemoteReplicationRequest(context.Background(), cli, payload)
	return err
}

func (c *cmdRemoteReplicationDisableRbd) prepareRbdPayload(requestType types.ReplicationRequestType, args []string) (types.RbdReplicationRequest, error) {
	pool, image, err := getPoolAndImageFromResource(args[0])
	if err != nil {
		return types.RbdReplicationRequest{}, err
	}

	retReq := types.RbdReplicationRequest{
		SourcePool:   pool,
		SourceImage:  image,
		RequestType:  requestType,
		IsForceOp:    c.isForce,
		ResourceType: getRbdResourceType(pool, image),
	}

	return retReq, nil
}
