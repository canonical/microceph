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

	// Replication RBD commands
	replicationRbdCmd := cmdRemoteReplicationRbd{common: c.common}
	cmd.AddCommand(replicationRbdCmd.Command())

	return cmd
}
