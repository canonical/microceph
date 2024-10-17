package main

import (
	"context"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdRemoteReplicationEnableRbd struct {
	common         *CmdControl
	remoteName     string
	repType        string
	schedule       string
	skipAutoEnable bool
}

func (c *cmdRemoteReplicationEnableRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enable <resource>",
		Short: "Enable remote replication for RBD resource (Pool or Image)",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.remoteName, "remote", "", "remote MicroCeph cluster name")
	cmd.MarkFlagRequired("remote")
	cmd.Flags().BoolVar(&c.skipAutoEnable, "skip-auto-enable", false, "do not auto enable rbd mirroring for all images in the pool.")
	cmd.Flags().StringVar(&c.repType, "type", "journal", "'journal' or 'snapshot', defaults to journal")
	cmd.Flags().StringVar(&c.schedule, "schedule", "", "snapshot schedule in days, hours, or minutes using d, h, m suffix respectively")
	return cmd
}

func (c *cmdRemoteReplicationEnableRbd) Run(cmd *cobra.Command, args []string) error {
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

	payload, err := c.prepareRbdPayload(types.EnableReplicationRequest, args)
	if err != nil {
		return err
	}

	_, err = client.SendRemoteReplicationRequest(context.Background(), cli, payload)
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdRemoteReplicationEnableRbd) prepareRbdPayload(requestType types.ReplicationRequestType, args []string) (types.RbdReplicationRequest, error) {
	pool, image, err := types.GetPoolAndImageFromResource(args[0])
	if err != nil {
		return types.RbdReplicationRequest{}, err
	}

	retReq := types.RbdReplicationRequest{
		RemoteName:      c.remoteName,
		SourcePool:      pool,
		SourceImage:     image,
		Schedule:        c.schedule,
		ReplicationType: types.RbdReplicationType(c.repType),
		RequestType:     requestType,
		ResourceType:    types.GetRbdResourceType(pool, image),
		SkipAutoEnable:  c.skipAutoEnable,
	}

	return retReq, nil
}
