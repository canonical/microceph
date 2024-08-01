package main

import (
	"context"
	"fmt"

	lxdCmd "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdClusterConfigList struct {
	common        *CmdControl
	cluster       *cmdCluster
	clusterConfig *cmdClusterConfig
}

func (c *cmdClusterConfigList) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all set Ceph level configs",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdClusterConfigList) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return fmt.Errorf("Unable to configure MicroCeph: %w", err)
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	// Create an empty Key request.
	req := &types.Config{
		Key: "",
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
