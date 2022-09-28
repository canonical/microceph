package main

import (
	"context"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdClusterRemove struct {
	common  *CmdControl
	cluster *cmdCluster
}

func (c *cmdClusterRemove) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <NAME>",
		Short: "Removes a server from the cluster",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdClusterRemove) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(context.Background(), c.common.FlagStateDir, c.common.FlagLogVerbose, c.common.FlagLogDebug)
	if err != nil {
		return err
	}

	client, err := m.LocalClient()
	if err != nil {
		return err
	}

	err = client.DeleteClusterMember(context.Background(), args[0])
	if err != nil {
		return err
	}

	return nil
}
