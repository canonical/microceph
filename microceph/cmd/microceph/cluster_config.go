package main

import (
	"github.com/spf13/cobra"
)

type cmdClusterConfig struct {
	common  *CmdControl
	cluster *cmdCluster
}

func (c *cmdClusterConfig) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage Ceph Cluster configs",
	}

	// Get
	clusterConfigGetCmd := cmdClusterConfigGet{common: c.common, cluster: c.cluster, clusterConfig: c}
	cmd.AddCommand(clusterConfigGetCmd.Command())

	// Set
	clusterConfigSetCmd := cmdClusterConfigSet{common: c.common, cluster: c.cluster, clusterConfig: c}
	cmd.AddCommand(clusterConfigSetCmd.Command())

	// Reset
	clusterConfigResetCmd := cmdClusterConfigReset{common: c.common, cluster: c.cluster, clusterConfig: c}
	cmd.AddCommand(clusterConfigResetCmd.Command())

	// List
	clusterConfigListCmd := cmdClusterConfigList{common: c.common, cluster: c.cluster, clusterConfig: c}
	cmd.AddCommand(clusterConfigListCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
