package main

import (
	"context"
	"strconv"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdPool struct {
	common *CmdControl
}

type cmdPoolSetRF struct {
	common *CmdControl
	poolRF *cmdPool
}

func (c *cmdPoolSetRF) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-rf <POOLS> <SIZE>",
		Short: "Set the replication factor for pools",
		Long: `Set the replication factor for <POOLS>
    POOLS is either a comma-separated list of pools such as pool1,pool2, an asterisk
    or an empty string. If it's an asterisk, the size is set for all existing pools,
    but not future ones, whereas an empty string implies setting the default pool size.
    Otherwise, the size is set for the specified pools.`,
		RunE: c.Run,
	}

	return cmd
}

func (c *cmdPoolSetRF) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return cmd.Help()
	}

	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	size, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		return err
	}

	req := &types.PoolPost{
		Pools: args[0],
		Size:  size,
	}

	return client.PoolSetReplicationFactor(context.Background(), cli, req)
}

func (c *cmdPool) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pool",
		Short: "Manage microceph pools",
	}

	// Set-RF.
	poolSetRFCmd := cmdPoolSetRF{common: c.common, poolRF: c}
	cmd.AddCommand(poolSetRFCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
