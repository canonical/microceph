package main

import (
	"context"

	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdEnableRBDMirror struct {
	common     *CmdControl
	wait       bool
	flagTarget string
}

func (c *cmdEnableRBDMirror) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rbd-mirror [--target <server>] [--wait <bool>]",
		Short: "Enable the RBD Mirror service on the --target server (default: this server)",
		RunE:  c.Run,
	}
	cmd.PersistentFlags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	cmd.Flags().BoolVar(&c.wait, "wait", true, "Wait for mon service to be up.")
	return cmd
}

// Run handles the enable mon command.
func (c *cmdEnableRBDMirror) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}
	cli = cli.UseTarget(c.flagTarget)
	req := &types.EnableService{
		Name:    "rbd-mirror",
		Wait:    c.wait,
		Payload: "",
	}

	err = client.SendServicePlacementReq(context.Background(), cli, req, c.flagTarget)
	if err != nil {
		return err
	}

	return nil
}
