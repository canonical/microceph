package main

import (
	"github.com/spf13/cobra"
)

type cmdReplicationRbd struct {
	common *CmdControl
}

func (c *cmdReplicationRbd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rbd",
		Short: "manage RBD replication to remote clusters",
	}

	// Replication enable command
	remoteReplicationRbdEnableCmd := cmdReplicationEnableRbd{common: c.common}
	cmd.AddCommand(remoteReplicationRbdEnableCmd.Command())

	// Replication disable command
	remoteReplicationRbdDisableCmd := cmdReplicationDisableRbd{common: c.common}
	cmd.AddCommand(remoteReplicationRbdDisableCmd.Command())

	// Replication list command
	remoteReplicationRbdListCmd := cmdReplicationListRbd{common: c.common}
	cmd.AddCommand(remoteReplicationRbdListCmd.Command())

	// Replication status command
	remoteReplicationRbdStatusCmd := cmdReplicationStatusRbd{common: c.common}
	cmd.AddCommand(remoteReplicationRbdStatusCmd.Command())

	// Replication configure command
	remoteReplicationRbdConfigureCmd := cmdReplicationConfigureRbd{common: c.common}
	cmd.AddCommand(remoteReplicationRbdConfigureCmd.Command())

	// Replication promote command
	remoteReplicationRbdPromoteCmd := cmdReplicationPromoteRbd{common: c.common}
	cmd.AddCommand(remoteReplicationRbdPromoteCmd.Command())

	// Replication demote command
	remoteReplicationRbdDemoteCmd := cmdReplicationDemoteRbd{common: c.common}
	cmd.AddCommand(remoteReplicationRbdDemoteCmd.Command())

	return cmd
}
