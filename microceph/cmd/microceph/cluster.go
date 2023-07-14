package main

import (
	"github.com/spf13/cobra"
)

type cmdCluster struct {
	common *CmdControl
}

func (c *cmdCluster) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage the MicroCeph cluster",
	}

	// Add
	clusterAddCmd := cmdClusterAdd{common: c.common, cluster: c}
	cmd.AddCommand(clusterAddCmd.Command())

	// Bootstrap
	clusterBootstrapCmd := cmdClusterBootstrap{common: c.common, cluster: c}
	cmd.AddCommand(clusterBootstrapCmd.Command())

	// Join
	clusterJoinCmd := cmdClusterJoin{common: c.common, cluster: c}
	cmd.AddCommand(clusterJoinCmd.Command())

	// List
	clusterListCmd := cmdClusterList{common: c.common, cluster: c}
	cmd.AddCommand(clusterListCmd.Command())

	// Remove
	clusterRemoveCmd := cmdClusterRemove{common: c.common, cluster: c}
	cmd.AddCommand(clusterRemoveCmd.Command())

	// SQL
	clusterSQLCmd := cmdClusterSQL{common: c.common, cluster: c}
	cmd.AddCommand(clusterSQLCmd.Command())

	// Config Subcommand
	clusterConfigCmd := cmdClusterConfig{common: c.common, cluster: c}
	cmd.AddCommand(clusterConfigCmd.Command())

	// Migrate Subcommand
	clusterMigrateCmd := cmdClusterMigrate{common: c.common, cluster: c}
	cmd.AddCommand(clusterMigrateCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
