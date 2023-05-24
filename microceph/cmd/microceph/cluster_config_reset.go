package main

import (
	"context"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdClusterConfigReset struct {
	common        *CmdControl
	cluster       *cmdCluster
	clusterConfig *cmdClusterConfig

	flagWait bool
}

func (c *cmdClusterConfigReset) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset <key>",
		Short: "Clear specified Ceph Cluster config",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.flagWait, "wait", false, "Wait for required ceph services to restart post config reset.")
	return cmd
}

func (c *cmdClusterConfigReset) Run(cmd *cobra.Command, args []string) error {
	allowList := ceph.GetConstConfigTable()
	if len(args) != 1 {
		return cmd.Help()
	}

	if _, ok := allowList[args[0]]; !ok {
		return fmt.Errorf("Resetting key %s is not allowed", args[0])
	}

	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return fmt.Errorf("Unable to configure MicroCeph: %w", err)
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	req := &types.Config{
		Key:  args[0],
		Wait: c.flagWait,
	}

	err = client.ClearConfig(context.Background(), cli, req)
	if err != nil {
		return err
	}

	return nil
}
