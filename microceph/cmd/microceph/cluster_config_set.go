package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdClusterConfigSet struct {
	common        *CmdControl
	cluster       *cmdCluster
	clusterConfig *cmdClusterConfig

	flagWait        bool
	flagSkipRestart bool
}

func (c *cmdClusterConfigSet) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <Key> <Value>",
		Short: "Set specified Ceph Cluster config",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.flagWait, "wait", false, "Wait for required ceph services to restart post config set.")
	cmd.Flags().BoolVar(&c.flagSkipRestart, "skip-restart", false, "Don't perform the daemon restart for current config.")
	return cmd
}

func (c *cmdClusterConfigSet) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return fmt.Errorf("unable to configure MicroCeph: %w", err)
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	req := &types.Config{
		Key:         args[0],
		Value:       args[1],
		Wait:        c.flagWait,
		SkipRestart: c.flagSkipRestart,
	}

	err = client.SetConfig(context.Background(), cli, req)
	if err != nil {
		if strings.Contains(err.Error(), "performed on experimental config") {
			fmt.Println(err.Error())
			return nil
		}
		return err
	}

	return nil
}
