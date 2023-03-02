package main

import (
	"context"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdEnableRGW struct {
	common     *CmdControl
	flagPort   int
	flagTarget string
}

func (c *cmdEnableRGW) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rgw [--port <port>] [--target <server>]",
		Short: "Enable the RGW service on this --target server (default: this server)",
		RunE:  c.Run,
	}
	cmd.PersistentFlags().IntVar(&c.flagPort, "port", 80, "Service port (default: 80)")
	cmd.PersistentFlags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	return cmd
}

// Run handles the enable rgw command.
func (c *cmdEnableRGW) Run(cmd *cobra.Command, args []string) error {

	m, err := microcluster.App(context.Background(), c.common.FlagStateDir, c.common.FlagLogVerbose, c.common.FlagLogDebug)
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}
	cli = cli.UseTarget(c.flagTarget)

	req := &types.RGWService{
		Port:    c.flagPort,
		Enabled: true,
	}

	err = client.EnableRGW(context.Background(), cli, req)
	if err != nil {
		return err
	}

	return nil
}
