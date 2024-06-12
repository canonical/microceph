package main

import (
	"context"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdEnableMGR struct {
	common     *CmdControl
	wait       bool
	flagTarget string
}

func (c *cmdEnableMGR) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mgr [--target <server>] [--wait <bool>]",
		Short: "Enable the MGR service on the --target server (default: this server)",
		RunE:  c.Run,
	}
	cmd.PersistentFlags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	cmd.Flags().BoolVar(&c.wait, "wait", true, "Wait for mgr service to be up.")
	return cmd
}

// Run handles the enable mgr command.
func (c *cmdEnableMGR) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	req := &types.EnableService{
		Name:    "mgr",
		Wait:    c.wait,
		Payload: "",
	}

	err = client.SendServicePlacementReq(context.Background(), cli, req, c.flagTarget)
	if err != nil {
		return err
	}

	return nil
}
