package main

import (
	"github.com/spf13/cobra"
)

type cmdReplicationList struct {
	common *CmdControl
}

func (c *cmdReplicationList) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all resources configured for replication.",
	}

	listRbdCmd := cmdReplicationListRbd{common: c.common}
	cmd.AddCommand(listRbdCmd.Command())

	listCephfsCmd := cmdReplicationListCephfs{common: c.common}
	cmd.AddCommand(listCephfsCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
