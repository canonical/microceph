package main

import (
	"github.com/spf13/cobra"
)

type cmdDisable struct {
	common *CmdControl
}

func (c *cmdDisable) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disables a feature on the cluster",
	}

	// Disable NFS
	disableNFSCmd := cmdDisableNFS{common: c.common}
	cmd.AddCommand(disableNFSCmd.Command())

	// Disable RGW
	disableRGWCmd := cmdDisableRGW{common: c.common}
	cmd.AddCommand(disableRGWCmd.Command())

	// Disable cephfs-mirror
	disableCephfsMirror := cmdDisableCephFSMirror{common: c.common}
	cmd.AddCommand(disableCephfsMirror.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
