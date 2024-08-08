package main

import (
	"github.com/spf13/cobra"
)

type cmdRemoteReplicationRbd struct {
	common *CmdControl
}

func (c *cmdRemoteReplicationRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rbd",
		Short: "manage RBD remote replication",
	}

	// Replication enable command
	remoteReplicationRbdEnableCmd := cmdRemoteReplicationEnableRbd{common: c.common}
	cmd.AddCommand(remoteReplicationRbdEnableCmd.Command())

	// Replication disable command
	remoteReplicationRbdDisableCmd := cmdRemoteReplicationDisableRbd{common: c.common}
	cmd.AddCommand(remoteReplicationRbdDisableCmd.Command())

	// Replication list command
	remoteReplicationRbdListCmd := cmdRemoteReplicationListRbd{common: c.common}
	cmd.AddCommand(remoteReplicationRbdListCmd.Command())

	// Replication status command
	remoteReplicationRbdStatusCmd := cmdRemoteReplicationStatusRbd{common: c.common}
	cmd.AddCommand(remoteReplicationRbdStatusCmd.Command())

	return cmd
}
