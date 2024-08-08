package main

import (
	"github.com/spf13/cobra"
)

type cmdRemoteReplicationList struct {
	common *CmdControl
}

func (c *cmdRemoteReplicationList) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all configured remotes replication pairs.",
	}

	// list RBD command
	remoteReplicationListRbdCmd := cmdRemoteReplicationListRbd{common: c.common}
	cmd.AddCommand(remoteReplicationListRbdCmd.Command())

	return cmd
}
