package main

import (
	"github.com/spf13/cobra"
)

type cmdRemoteReplicationDisable struct {
	common *CmdControl
}

func (c *cmdRemoteReplicationDisable) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable remote replication for workloads",
	}

	// Disable RBD command.
	remoteReplicationDisableRbdCmd := cmdRemoteReplicationDisableRbd{common: c.common}
	cmd.AddCommand(remoteReplicationDisableRbdCmd.Command())

	return cmd
}
