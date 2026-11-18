package main

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	lxdCmd "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/v3/microcluster"
)

type cmdPool struct {
	common *CmdControl
}

type cmdPoolSetRF struct {
	common         *CmdControl
	poolRF         *cmdPool
	poolSize       int64
	flagSetDefault bool
}

func (c *cmdPoolSetRF) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-rf --size <SIZE> [--set-default] [<POOL>...]",
		Short: "Set the replication factor for pools",
		Long: `Set the replication factor for <POOLS>
    POOLS is either a list of pools an asterisk or an empty string.
    If it's an asterisk, the size is set for all existing pools, but not
    future ones, whereas an empty string implies setting the default pool size.
    Otherwise, the size is set for the specified pools.
    With --set-default, the default pool size is set as well, and POOLS
    may be omitted.`,
		Args: c.validateArgs,
		RunE: c.Run,
	}

	cmd.Flags().Int64Var(&c.poolSize, "size", 3, "Pool size")
	_ = cmd.MarkFlagRequired("size")
	cmd.Flags().BoolVar(&c.flagSetDefault, "set-default", false, "Also set the default size of pools created later")

	return cmd
}

// validateArgs is the positional argument validator of "pool set-rf". The pools may be omitted
// when --set-default is given, since setting the default pool size alone is then a complete request.
func (c *cmdPoolSetRF) validateArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 && !c.flagSetDefault {
		return errors.New("no pools given: pass one or more pools, '*' or use --set-default")
	}

	return nil
}

// setsDefault reports whether "pool set-rf" sets the default pool size: either --set-default was
// given or one of the pools is an empty string, which has always meant the default pool size.
func setsDefault(flagSetDefault bool, pools []string) bool {
	if flagSetDefault {
		return true
	}

	for _, pool := range pools {
		if strings.TrimSpace(pool) == "" {
			return true
		}
	}

	return false
}

func (c *cmdPoolSetRF) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	setDefault := setsDefault(c.flagSetDefault, args)
	req := &types.PoolPut{
		Pools:      args,
		Size:       c.poolSize,
		SetDefault: &setDefault,
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
		Args:    cobra.NoArgs,
		RunE:    c.Run,
	}

	return cmd
}

func (c *cmdPoolList) Run(cmd *cobra.Command, args []string) error {
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
