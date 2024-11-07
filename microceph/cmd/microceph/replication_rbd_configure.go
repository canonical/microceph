package main

import (
	"context"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdReplicationConfigureRbd struct {
	common   *CmdControl
	schedule string
}

func (c *cmdReplicationConfigureRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure <resource>",
		Short: "Configure replication parameters for RBD resource (Pool or Image)",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.schedule, "schedule", "", "snapshot schedule in days, hours, or minutes using d, h, m suffix respectively")
	return cmd
}

func (c *cmdReplicationConfigureRbd) Run(cmd *cobra.Command, args []string) error {
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

	payload, err := c.prepareRbdPayload(types.ConfigureReplicationRequest, args)
	if err != nil {
		return err
	}

	_, err = client.SendReplicationRequest(context.Background(), cli, payload)
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdReplicationConfigureRbd) prepareRbdPayload(requestType types.ReplicationRequestType, args []string) (types.RbdReplicationRequest, error) {
	pool, image, err := types.GetPoolAndImageFromResource(args[0])
	if err != nil {
		return types.RbdReplicationRequest{}, err
	}

	retReq := types.RbdReplicationRequest{
		SourcePool:   pool,
		SourceImage:  image,
		Schedule:     c.schedule,
		RequestType:  requestType,
		ResourceType: types.GetRbdResourceType(pool, image),
	}

	return retReq, nil
}
