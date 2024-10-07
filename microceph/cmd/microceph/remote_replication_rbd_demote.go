package main

import (
	"context"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdRemoteReplicationDemoteRbd struct {
	common     *CmdControl
	remoteName string
	isForce    bool
}

func (c *cmdRemoteReplicationDemoteRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "demote",
		Short: "Demote a primary cluster to non-primary status",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.remoteName, "remote", "", "remote MicroCeph cluster name")
	cmd.Flags().BoolVar(&c.isForce, "yes-i-really-mean-it", false, "demote cluster irrespective of data loss")
	cmd.MarkFlagRequired("remote")
	return cmd
}

func (c *cmdRemoteReplicationDemoteRbd) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
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

	payload, err := c.prepareRbdPayload(types.DemoteReplicationRequest)
	if err != nil {
		return err
	}

	_, err = client.SendRemoteReplicationRequest(context.Background(), cli, payload)
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdRemoteReplicationDemoteRbd) prepareRbdPayload(requestType types.ReplicationRequestType) (types.RbdReplicationRequest, error) {
	retReq := types.RbdReplicationRequest{
		RemoteName:   c.remoteName,
		RequestType:  requestType,
		ResourceType: types.RbdResourcePool,
		SourcePool:   "",
		IsForceOp:    c.isForce,
	}

	return retReq, nil
}
