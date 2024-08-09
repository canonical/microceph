package main

import (
	"github.com/spf13/cobra"
)

type cmdRemoteReplication struct {
	common *CmdControl
}

func (c *cmdRemoteReplication) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "replication",
		Short: "manage remote replication",
	}

	// Replication enable command
	replicationEnableCmd := cmdRemoteReplicationEnable{common: c.common}
	cmd.AddCommand(replicationEnableCmd.Command())

	// Replication disable command
	replicationDisableCmd := cmdRemoteReplicationDisable{common: c.common}
	cmd.AddCommand(replicationDisableCmd.Command())

	// Replication list command
	replicationListCmd := cmdRemoteReplicationList{common: c.common}
	cmd.AddCommand(replicationListCmd.Command())

	// Replication status command
	replicationStatusCmd := cmdRemoteReplicationStatus{common: c.common}
	cmd.AddCommand(replicationStatusCmd.Command())

	return cmd
}
