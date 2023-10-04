package main

import (
	"github.com/spf13/cobra"
)

type cmdClientConfig struct {
	common *CmdControl
	client *cmdClient
}

func (c *cmdClientConfig) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage Ceph Client configs",
	}

	// Get
	clientConfigGetCmd := cmdClientConfigGet{common: c.common, client: c.client, clientConfig: c}
	cmd.AddCommand(clientConfigGetCmd.Command())

	// Set
	clientConfigSetCmd := cmdClientConfigSet{common: c.common, client: c.client, clientConfig: c}
	cmd.AddCommand(clientConfigSetCmd.Command())

	// Reset
	clientConfigResetCmd := cmdClientConfigReset{common: c.common, client: c.client, clientConfig: c}
	cmd.AddCommand(clientConfigResetCmd.Command())

	// List
	clientConfigListCmd := cmdClientConfigList{common: c.common, client: c.client, clientConfig: c}
	cmd.AddCommand(clientConfigListCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
