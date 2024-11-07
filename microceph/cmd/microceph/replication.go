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

	// Replication RBD commands
	replicationRbdCmd := cmdReplicationRbd{common: c.common}
	cmd.AddCommand(replicationRbdCmd.Command())

	return cmd
}
