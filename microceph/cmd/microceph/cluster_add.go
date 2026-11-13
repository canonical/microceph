package main

import (
	"fmt"

	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdClusterAdd struct {
	common  *CmdControl
	cluster *cmdCluster
}

func (c *cmdClusterAdd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <NAME>",
		Short: "Generates a token for a new server",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdClusterAdd) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	// Preserve Quincy's non-expiring, single-use join-token policy.
	token, err := m.NewJoinToken(cmd.Context(), args[0], 0)
	if err != nil {
		return err
	}

	fmt.Println(token)

	return nil
}
