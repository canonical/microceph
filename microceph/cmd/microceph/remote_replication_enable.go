package main

import (
	"github.com/spf13/cobra"
)

type cmdRemoteReplicationEnable struct {
	common *CmdControl
}

func (c *cmdRemoteReplicationEnable) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Enable remote replication for workloads",
	}

	// Enable RBD command
	remoteReplicationEnableRbdCmd := cmdRemoteReplicationEnableRbd{common: c.common}
	cmd.AddCommand(remoteReplicationEnableRbdCmd.Command())
	return cmd
}
