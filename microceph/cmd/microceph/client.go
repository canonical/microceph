package main

import (
	"github.com/spf13/cobra"
)

type cmdClient struct {
	common *CmdControl
	client *cmdClient
}

func (c *cmdClient) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "client",
		Short: "Manage the MicroCeph clients",
	}

	// Config Subcommand
	clientConfigCmd := cmdClientConfig{common: c.common, client: c}
	cmd.AddCommand(clientConfigCmd.Command())

	// S3 Subcommand
	clientS3Cmd := cmdClientS3{common: c.common, client: c.client}
	cmd.AddCommand(clientS3Cmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
