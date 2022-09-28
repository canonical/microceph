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
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdEnable) Run(cmd *cobra.Command, args []string) error {
	return nil
}
