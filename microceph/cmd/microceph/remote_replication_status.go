package main

import (
	"github.com/spf13/cobra"
)

type cmdRemoteReplicationStatus struct {
	common *CmdControl
}

func (c *cmdRemoteReplicationStatus) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show remote replication pair status",
	}

	// status Rbd command
	remoteReplicationStatusRbdCmd := cmdRemoteReplicationStatusRbd{common: c.common}
	cmd.AddCommand(remoteReplicationStatusRbdCmd.Command())

	return cmd
}
