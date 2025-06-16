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
		Short: "Enables a feature or service on the cluster",
	}

	// Enable RGW
	enableRGWCmd := cmdEnableRGW{common: c.common}
	enableMonCmd := cmdEnableMON{common: c.common}
	enableMgrCmd := cmdEnableMGR{common: c.common}
	enableMdsCmd := cmdEnableMDS{common: c.common}
	enableRbdMirrorCmd := cmdEnableRBDMirror{common: c.common}
	enableCephExporterCmd := cmdEnableCephExporter{common: c.common}

	cmd.AddCommand(enableRGWCmd.Command())
	cmd.AddCommand(enableMonCmd.Command())
	cmd.AddCommand(enableMgrCmd.Command())
	cmd.AddCommand(enableMdsCmd.Command())
	cmd.AddCommand(enableRbdMirrorCmd.Command())
	cmd.AddCommand(enableCephExporterCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
