package main

import (
	"context"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdRemoteReplicationConfigureRbd struct {
	common    *CmdControl
	poolName  string
	imageName string
	repType   string
	schedule  string
}

func (c *cmdRemoteReplicationConfigureRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Configure remote replication for RBD Pool or Image",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.poolName, "pool", "", "RBD pool name")
	cmd.MarkFlagRequired("pool")
	cmd.Flags().StringVar(&c.imageName, "image", "", "RBD image name")
	cmd.Flags().StringVar(&c.schedule, "snapshot-schedule", "", "snapshot schedule in days, hours, or minutes using d, h, m suffix respectively")
	return cmd
}

func (c *cmdRemoteReplicationConfigureRbd) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
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

	payload, err := c.prepareRbdPayload(types.ConfigureReplicationRequest)
	if err != nil {
		return err
	}

	// TODO: configure request does not expect any response.
	resp, err := client.SendRemoteReplicationRequest(context.Background(), cli, payload)
	if err == nil {
		fmt.Println(resp)
	}

	return err
}

func (c *cmdRemoteReplicationConfigureRbd) prepareRbdPayload(requestType types.ReplicationRequestType) (types.RbdReplicationRequest, error) {
	retReq := types.RbdReplicationRequest{
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
