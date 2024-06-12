package main

import (
	"context"
	"fmt"

	lxdCmd "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdClusterConfigGet struct {
	common        *CmdControl
	cluster       *cmdCluster
	clusterConfig *cmdClusterConfig
}

func (c *cmdClusterConfigGet) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Get specified Ceph Cluster config",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdClusterConfigGet) Run(cmd *cobra.Command, args []string) error {
	allowList := ceph.GetConstConfigTable()

	// Get can be called with a single key.
	if len(args) != 1 {
		return cmd.Help()
	}

	if _, ok := allowList[args[0]]; !ok {
		return fmt.Errorf("Key %s is invalid. \nPermitted Keys: %v", args[0], allowList.Keys())
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return fmt.Errorf("Unable to configure MicroCeph: %w", err)
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	req := &types.Config{
		Key: args[0],
	}

	configs, err := client.GetConfig(context.Background(), cli, req)
	if err != nil {
		return err
	}

	data := make([][]string, len(configs))
	for i, config := range configs {
		data[i] = []string{fmt.Sprintf("%d", i), config.Key, config.Value}
	}

	header := []string{"#", "Key", "Value"}
	err = lxdCmd.RenderTable(lxdCmd.TableFormatTable, header, data, configs)
	if err != nil {
		return err
	}

	return nil
}
