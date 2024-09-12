package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v2/microcluster"
)

type cmdPool struct {
	common *CmdControl
}

type cmdPoolSetRF struct {
	common   *CmdControl
	poolRF   *cmdPool
	poolSize int64
}

func (c *cmdPoolSetRF) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-rf <SIZE> <POOL>...",
		Short: "Set the replication factor for pools",
		Long: `Set the replication factor for <POOLS>
    POOLS is either a list of pools an asterisk or an empty string.
    If it's an asterisk, the size is set for all existing pools, but not
    future ones, whereas an empty string implies setting the default pool size.
    Otherwise, the size is set for the specified pools.`,
		RunE: c.Run,
	}

	cmd.Flags().Int64Var(&c.poolSize, "size", 3, "Pool size")
	cmd.MarkFlagRequired("size")

	return cmd
}

func (c *cmdPoolSetRF) Run(cmd *cobra.Command, args []string) error {
	if len(args) < 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	req := &types.PoolPut{
		Pools: args,
		Size:  c.poolSize,
	}

	return client.PoolSetReplicationFactor(context.Background(), cli, req)
}

func (c *cmdPool) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pool",
		Short: "Manage microceph pools",
	}

	// set-rf.
	poolSetRFCmd := cmdPoolSetRF{common: c.common, poolRF: c}
	cmd.AddCommand(poolSetRFCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
