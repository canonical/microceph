package main

import (
	"context"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdReplicationDisableRbd struct {
	common  *CmdControl
	isForce bool
}

func (c *cmdReplicationDisableRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rbd <resource>",
		Short: "Disable replication for RBD resource",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.isForce, "force", false, "forcefully disable replication for rbd resource")
	return cmd
}

func (c *cmdReplicationDisableRbd) Run(cmd *cobra.Command, args []string) error {
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

	_, err = client.SendReplicationRequest(context.Background(), cli, payload)
	return err
}

func (c *cmdReplicationDisableRbd) prepareRbdPayload(requestType types.ReplicationRequestType, args []string) (types.RbdReplicationRequest, error) {
	pool, image, err := types.GetPoolAndImageFromResource(args[0])
	if err != nil {
		return types.RbdReplicationRequest{}, err
	}

	retReq := types.RbdReplicationRequest{
		SourcePool:   pool,
		SourceImage:  image,
		RequestType:  requestType,
		IsForceOp:    c.isForce,
		ResourceType: types.GetRbdResourceType(pool, image),
	}

	return retReq, nil
}
