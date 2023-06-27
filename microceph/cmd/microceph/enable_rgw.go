package main

import (
	"context"
	"encoding/json"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
)

type cmdEnableRGW struct {
	common     *CmdControl
	wait       bool
	flagPort   int
	flagTarget string
}

func (c *cmdEnableRGW) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rgw [--port <port>] [--target <server>] [--wait <bool>]",
		Short: "Enable the RGW service on the --target server (default: this server)",
		RunE:  c.Run,
	}
	cmd.PersistentFlags().IntVar(&c.flagPort, "port", 80, "Service port (default: 80)")
	cmd.PersistentFlags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	cmd.Flags().BoolVar(&c.wait, "wait", true, "Wait for rgw service to be up.")
	return cmd
}

// Run handles the enable rgw command.
func (c *cmdEnableRGW) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	jsp, err := json.Marshal(ceph.RgwServicePlacement{Port: c.flagPort})
	if err != nil {
		return err
	}

	req := &types.EnableService{
		Name:    "rgw",
		Wait:    c.wait,
		Payload: string(jsp[:]),
	}

	err = client.SendServicePlacementReq(context.Background(), cli, req, c.flagTarget)
	if err != nil {
		return err
	}

	return nil
}
