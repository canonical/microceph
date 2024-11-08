package main

import (
	"context"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdReplicationPromote struct {
	common     *CmdControl
	remoteName string
	isForce    bool
}

func (c *cmdReplicationPromote) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "promote",
		Short: "Promote a non-primary cluster to primary status",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.remoteName, "remote", "", "remote MicroCeph cluster name")
	cmd.Flags().BoolVar(&c.isForce, "yes-i-really-mean-it", false, "forcefully promote site to primary")
	cmd.MarkFlagRequired("remote")
	return cmd
}

func (c *cmdReplicationPromote) Run(cmd *cobra.Command, args []string) error {
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

	payload, err := c.preparePayload(types.PromoteReplicationRequest)
	if err != nil {
		return err
	}

	_, err = client.SendReplicationRequest(context.Background(), cli, payload)
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdReplicationPromote) preparePayload(requestType types.ReplicationRequestType) (types.RbdReplicationRequest, error) {
	retReq := types.RbdReplicationRequest{
		RemoteName:   c.remoteName,
		RequestType:  requestType,
		IsForceOp:    c.isForce,
		ResourceType: types.RbdResourcePool,
		SourcePool:   "",
	}

	return retReq, nil
}
