package main

import (
	"github.com/spf13/cobra"
)

type cmdReplicationStatus struct {
	common *CmdControl
}

func (c *cmdReplicationStatus) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show resource replication status",
	}

	statusRbdCmd := cmdReplicationStatusRbd{common: c.common}
	cmd.AddCommand(statusRbdCmd.Command())

	statusCephfsCmd := cmdReplicationStatusCephfs{common: c.common}
	cmd.AddCommand(statusCephfsCmd.Command())

	return cmd
}
