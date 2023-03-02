package main

import (
	"github.com/spf13/cobra"
)

type cmdEnable struct {
	common *CmdControl
}

func (c *cmdEnable) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Enables a feature on the cluster",
	}

	// Enable RGW
	enableRGWCmd := cmdEnableRGW{common: c.common}
	cmd.AddCommand(enableRGWCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
