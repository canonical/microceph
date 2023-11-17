package main

import (
	"context"
	"fmt"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdClusterConfigSet struct {
	common        *CmdControl
	cluster       *cmdCluster
	clusterConfig *cmdClusterConfig

	flagWait  bool
	flagForce bool
}

func (c *cmdClusterConfigSet) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <Key> <Value>",
		Short: "Set specified Ceph Cluster config",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.flagWait, "wait", false, "Wait for required ceph services to restart post config set.")
	cmd.Flags().BoolVar(&c.flagForce, "yes-i-really-mean-it", false, "Force microceph to set the config key.")
	return cmd
}

func (c *cmdClusterConfigSet) Run(cmd *cobra.Command, args []string) error {
	allowList := ceph.GetConstConfigTable()
	if len(args) != 2 {
		return cmd.Help()
	}

	config, ok := allowList[args[0]]
	if !ok {
		return fmt.Errorf("configuring key %s is not allowed. \nPermitted Keys: %v", args[0], allowList.Keys())
	}

	if config.NeedForced && !c.flagForce {
		return fmt.Errorf("configuring key %s is not allowed. %s", args[0], common.CliForcePrompt)
	}

	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return fmt.Errorf("unable to configure MicroCeph: %w", err)
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	req := &types.Config{
		Key:   args[0],
		Value: args[1],
		Wait:  c.flagWait,
		Force: c.flagForce,
	}

	err = client.SetConfig(context.Background(), cli, req)
	if err != nil {
		return err
	}

	return nil
}
