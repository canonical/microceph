package main

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdClusterAdd struct {
	common  *CmdControl
	cluster *cmdCluster

	flagTokenDuration float64
}

func (c *cmdClusterAdd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <NAME>",
		Short: "Generates a token for a new server",
		RunE:  c.Run,
	}

	cmd.Flags().Float64Var(&c.flagTokenDuration, "timeout", time.Duration(3*time.Hour).Seconds(), "Number of seconds the token will be valid for (Default: 3 hours)")

	return cmd
}

func (c *cmdClusterAdd) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	token, err := m.NewJoinToken(context.Background(), args[0], time.Duration(c.flagTokenDuration*float64(time.Second)))
	if err != nil {
		return err
	}

	fmt.Println(token)

	return nil
}
