package main

import (
	"github.com/spf13/cobra"
)

type cmdClusterMaintenance struct {
	common *CmdControl
}

func (c *cmdClusterMaintenance) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "maintenance",
		Short: "Enter or exit the maintenance mode.",
	}

	// Exit
	clusterMaintenanceExit := cmdClusterMaintenanceExit{common: c.common}
	cmd.AddCommand(clusterMaintenanceExit.Command())

	// Enter
	clusterMaintenanceEnter := cmdClusterMaintenanceEnter{common: c.common}
	cmd.AddCommand(clusterMaintenanceEnter.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
