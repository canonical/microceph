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

type cmdClientConfigSet struct {
	common       *CmdControl
	client       *cmdClient
	clientConfig *cmdClientConfig

	flagWait bool
	flagHost string
}

func (c *cmdClientConfigSet) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <Key> <Value>",
		Short: "Sets specified Ceph Client config",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.flagWait, "wait", true, "Wait for configs to propagate across the cluster.")
	// * stands for global configs, hence all configs are global by default unless specifies.
	cmd.Flags().StringVar(&c.flagHost, "target", "*", "Specify a microceph node the provided config should be applied to.")
	return cmd
}

func (c *cmdClientConfigSet) Run(cmd *cobra.Command, args []string) error {
	allowList := ceph.GetClientConfigSet()
	if len(args) != 2 {
		return cmd.Help()
	}

	_, ok := allowList[args[0]]
	if !ok {
		return fmt.Errorf("configuring key %s is not supported.\nSupported Keys: %v", args[0], allowList.Keys())
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return fmt.Errorf("unable to configure MicroCeph: %w", err)
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	req := &types.ClientConfig{
		Key:   args[0],
		Value: args[1],
		Wait:  c.flagWait,
		Host:  c.flagHost,
	}

	err = client.SetClientConfig(context.Background(), cli, req)
	if err != nil {
		return err
	}

	return nil
}
