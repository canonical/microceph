package main

import (
	"context"
	"fmt"
	"github.com/canonical/microceph/microceph/constants"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdClientConfigReset struct {
	common       *CmdControl
	client       *cmdClient
	clientConfig *cmdClientConfig

	flagWait  bool
	flagHost  string
	flagForce bool
}

func (c *cmdClientConfigReset) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset <key>",
		Short: "Removes specified Ceph Client configs",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.flagWait, "wait", true, "Wait for required ceph services to restart post config reset.")
	// * stands for global configs, hence all configs are global by default unless specifies.
	cmd.Flags().StringVar(&c.flagHost, "target", "*", "Specify a microceph node the provided config should be applied to.")
	cmd.Flags().BoolVar(&c.flagForce, "yes-i-really-mean-it", false, "Force microceph to reset all client configs records for given key.")
	return cmd
}

func (c *cmdClientConfigReset) Run(cmd *cobra.Command, args []string) error {
	allowList := ceph.GetClientConfigSet()
	if len(args) != 1 {
		return cmd.Help()
	}

	_, ok := allowList[args[0]]
	if !ok {
		return fmt.Errorf("resetting key %s is not supported.\nSupported Keys: %v", args[0], allowList.Keys())
	}

	if !c.flagForce {
		return fmt.Errorf("WARNING: this will *PERMANENTLY REMOVE* all records of the %s key. %s",
			args[0], constants.CliForcePrompt)
	}

	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return fmt.Errorf("unable to configure MicroCeph: %w", err)
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	req := &types.ClientConfig{
		Key:  args[0],
		Wait: c.flagWait,
		Host: c.flagHost,
	}

	err = client.ResetClientConfig(context.Background(), cli, req)
	if err != nil {
		return err
	}

	return nil
}
