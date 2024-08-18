package main

import (
	"context"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdRemoteReplicationDisableRbd struct {
	common    *CmdControl
	poolName  string
	imageName string
}

func (c *cmdRemoteReplicationDisableRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable remote replication for RBD Pool or Image",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.poolName, "pool", "", "RBD pool name")
	cmd.Flags().StringVar(&c.imageName, "image", "", "RBD image name")
	return cmd
}

func (c *cmdRemoteReplicationDisableRbd) Run(cmd *cobra.Command, args []string) error {
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

	payload, err := c.prepareRbdPayload(types.DisableReplicationRequest)
	if err != nil {
		return err
	}

	return client.SendRemoteReplicationRequest(context.Background(), cli, payload)
}

func (c *cmdRemoteReplicationDisableRbd) prepareRbdPayload(requestType types.ReplicationRequestType) (types.RbdReplicationRequest, error) {
	retReq := types.RbdReplicationRequest{
		SourcePool:  c.poolName,
		SourceImage: c.imageName,
		RequestType: requestType,
	}

	if len(c.poolName) != 0 && len(c.imageName) != 0 {
		retReq.ResourceType = types.RbdResourceImage
	} else {
		retReq.ResourceType = types.RbdResourcePool
	}

	return retReq, nil
}
