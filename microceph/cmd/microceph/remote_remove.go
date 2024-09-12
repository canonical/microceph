package main

import (
	"context"

	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdRemoteRemove struct {
	common *CmdControl
}

func (c *cmdRemoteRemove) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove configured remote",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdRemoteRemove) Run(cmd *cobra.Command, args []string) error {
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

	// send remote remove request
	return client.SendRemoteRemoveRequest(context.Background(), cli, args[0])
}
