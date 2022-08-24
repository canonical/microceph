package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

type cmdInit struct {
	common *CmdControl

	flagBootstrap bool
	flagToken     string
}

func (c *cmdInit) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Interactive configuration of MicroCeph",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdInit) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("Not implemented")
}
