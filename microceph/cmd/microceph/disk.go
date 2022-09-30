package main

import (
	"github.com/spf13/cobra"
)

type cmdDisk struct {
	common *CmdControl
}

func (c *cmdDisk) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disk",
		Short: "Manage the MicroCeph disks",
	}

	// Add
	diskAddCmd := cmdDiskAdd{common: c.common, disk: c}
	cmd.AddCommand(diskAddCmd.Command())

	// List
	diskListCmd := cmdDiskList{common: c.common, disk: c}
	cmd.AddCommand(diskListCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
