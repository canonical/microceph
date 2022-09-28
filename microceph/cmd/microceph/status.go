package main

import (
	"github.com/spf13/cobra"
)

type cmdStatus struct {
	common *CmdControl
}

func (c *cmdStatus) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Checks the cluster status",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdStatus) Run(cmd *cobra.Command, args []string) error {
	return nil
}
