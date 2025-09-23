package main

import (
	"github.com/spf13/cobra"
)

type cmdReplicationDisable struct {
	common *CmdControl
}

func (c *cmdReplicationDisable) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable replication",
	}

	disableRbdCmd := cmdReplicationDisableRbd{common: c.common}
	cmd.AddCommand(disableRbdCmd.Command())

	disableCephFSCmd := cmdReplicationDisableCephFS{common: c.common}
	cmd.AddCommand(disableCephFSCmd.Command())

	return cmd
}
