package main

import (
	"context"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdRemoteReplicationStatusRbd struct {
	common    *CmdControl
	poolName  string
	imageName string
	json      bool
}

func (c *cmdRemoteReplicationStatusRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show RBD Pool or Image replication status",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.poolName, "pool", "", "RBD pool name")
	cmd.Flags().StringVar(&c.imageName, "image", "", "RBD image name")
	cmd.Flags().BoolVar(&c.json, "json", false, "output as json string")
	return cmd
}

func (c *cmdRemoteReplicationStatusRbd) Run(cmd *cobra.Command, args []string) error {
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

	payload, err := c.prepareRbdPayload(types.StatusReplicationRequest)
	if err != nil {
		return err
	}

	resp, err := client.SendRemoteReplicationRequest(context.Background(), cli, payload)
	if err == nil {
		fmt.Println(resp)
	}

	return err
}

func (c *cmdRemoteReplicationStatusRbd) prepareRbdPayload(requestType types.ReplicationRequestType) (types.RbdReplicationRequest, error) {
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
