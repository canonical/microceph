package main

import (
	"github.com/spf13/cobra"
)

type cmdReplicationEnable struct {
	common *CmdControl
}

func (c *cmdReplicationEnable) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Enable replication for a workload",
	}

	enableRbdCmd := cmdReplicationEnableRbd{common: c.common}
	cmd.AddCommand(enableRbdCmd.Command())

	enableCephFSCmd := cmdReplicationEnableCephFS{common: c.common}
	cmd.AddCommand(enableCephFSCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
