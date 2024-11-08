package main

import (
	"github.com/spf13/cobra"
)

type cmdReplication struct {
	common *CmdControl
}

func (c *cmdReplication) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "replication",
		Short: "manage replication to remote clusters",
	}

	// Replication enable command
	replicationEnableCmd := cmdReplicationEnable{common: c.common}
	cmd.AddCommand(replicationEnableCmd.Command())

	// Replication disable command
	replicationDisableCmd := cmdReplicationDisable{common: c.common}
	cmd.AddCommand(replicationDisableCmd.Command())

	// Replication list command
	replicationListCmd := cmdReplicationList{common: c.common}
	cmd.AddCommand(replicationListCmd.Command())

	// Replication status command
	replicationStatusCmd := cmdReplicationStatus{common: c.common}
	cmd.AddCommand(replicationStatusCmd.Command())

	// Replication configure command
	replicationConfigureCmd := cmdReplicationConfigure{common: c.common}
	cmd.AddCommand(replicationConfigureCmd.Command())

	// Replication promote command
	replicationPromoteCmd := cmdReplicationPromote{common: c.common}
	cmd.AddCommand(replicationPromoteCmd.Command())

	// Replication demote command
	replicationDemoteCmd := cmdReplicationDemote{common: c.common}
	cmd.AddCommand(replicationDemoteCmd.Command())

	return cmd
}
