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

	flagTokenDuration string
}

func (c *cmdClusterAdd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <NAME>",
		Short: "Generates a token for a new server",
		RunE:  c.Run,
	}

	cmd.Flags().StringVarP(&c.flagTokenDuration, "timeout", "t", "3h", "Set the lifetime for the token. Default is 3 hours. (eg. 10s, 5m, 3h)")

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

	expireAfter, err := time.ParseDuration(c.flagTokenDuration)
	if err != nil {
		return fmt.Errorf("Invalid value for timeout flag: %w", err)
	}

	token, err := m.NewJoinToken(context.Background(), args[0], expireAfter)
	if err != nil {
		return err
	}

	fmt.Println(token)

	return nil
}
