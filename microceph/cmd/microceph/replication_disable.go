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
		Short: "Disable replication for a workload",
	}

	disableRbdCmd := cmdReplicationDisableRbd{common: c.common}
	cmd.AddCommand(disableRbdCmd.Command())

	disableCephFSCmd := cmdReplicationDisableCephFS{common: c.common}
	cmd.AddCommand(disableCephFSCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
