package main

import (
	"github.com/spf13/cobra"
)

type cmdRemote struct {
	common *CmdControl
}

func (c *cmdRemote) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Manage MicroCeph remotes",
	}

	// Import subcommand
	remoteImportCmd := cmdRemoteImport{common: c.common}
	cmd.AddCommand(remoteImportCmd.Command())
	// List subcommand
	remoteListCmd := cmdRemoteList{common: c.common}
	cmd.AddCommand(remoteListCmd.Command())
	// Remove subcommand
	remoteRemoveCmd := cmdRemoteRemove{common: c.common}
	cmd.AddCommand(remoteRemoveCmd.Command())
	// Replication subcommand
	remoteReplicationCmd := cmdRemoteReplication{common: c.common}
	cmd.AddCommand(remoteReplicationCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
