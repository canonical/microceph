package main

import (
	"context"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdRemoteReplicationEnableRbd struct {
	common    *CmdControl
	poolName  string
	imageName string
	repType   string
	schedule  string
}

func (c *cmdRemoteReplicationEnableRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enable <remote>",
		Short: "Enable remote replication for RBD Pool or Image",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.poolName, "pool", "", "RBD pool name")
	cmd.MarkFlagRequired("pool")
	cmd.Flags().StringVar(&c.imageName, "image", "", "RBD image name")
	cmd.Flags().StringVar(&c.repType, "type", "journal", "'journal' or 'snapshot', defaults to journal")
	cmd.Flags().StringVar(&c.schedule, "schedule", "", "snapshot schedule in days, hours, or minutes using d, h, m suffix respectively")
	return cmd
}

func (c *cmdRemoteReplicationEnableRbd) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
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

	// TODO: Enable request does not expect any response.
	resp, err := client.SendRemoteReplicationRequest(context.Background(), cli, payload)
	if err == nil {
		fmt.Println(resp)
	}

	return err
}

func (c *cmdRemoteReplicationEnableRbd) prepareRbdPayload(requestType types.ReplicationRequestType, args []string) (types.RbdReplicationRequest, error) {
	retReq := types.RbdReplicationRequest{
		RemoteName:      args[0],
		SourcePool:      c.poolName,
		SourceImage:     c.imageName,
		Schedule:        c.schedule,
		ReplicationType: types.RbdReplicationType(c.repType),
		RequestType:     requestType,
	}

	if len(c.poolName) != 0 && len(c.imageName) != 0 {
		retReq.ResourceType = types.RbdResourceImage
	} else {
		retReq.ResourceType = types.RbdResourcePool
	}

	return retReq, nil
}
