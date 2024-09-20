package main

import (
	"context"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	lxdCmd "github.com/canonical/lxd/shared/cmd"
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

type cmdPoolList struct {
	common *CmdControl
}

func (c *cmdPoolList) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List information about OSD pools",
		RunE:    c.Run,
	}

	return cmd
}

func (c *cmdPoolList) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
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

	pools, err := client.GetPools(cmd.Context(), cli)
	if err != nil {
		return err
	}

	data := make([][]string, len(pools))
	for i, pool := range pools {
		data[i] = []string{pool.Pool, strconv.Itoa(int(pool.Size)), pool.CrushRule}
	}

	header := []string{"NAME", "SIZE", "CRUSH RULE"}
	sort.Sort(lxdCmd.SortColumnsNaturally(data))

	return lxdCmd.RenderTable(lxdCmd.TableFormatTable, header, data, pools)

}

func (c *cmdPool) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pool",
		Short: "Manage microceph pools",
	}

	// set-rf.
	poolSetRFCmd := cmdPoolSetRF{common: c.common, poolRF: c}
	cmd.AddCommand(poolSetRFCmd.Command())

	// list.
	poolListCmd := cmdPoolList{common: c.common}
	cmd.AddCommand(poolListCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
