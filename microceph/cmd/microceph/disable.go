package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

type cmdDisable struct {
	common *CmdControl
}

func (c *cmdDisable) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disables a feature on the cluster",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdDisable) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("MicroCeph doesn't currently have optional services")
}
