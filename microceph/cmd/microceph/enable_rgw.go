package main

import (
	"context"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdEnableRGW struct {
	common    *CmdControl
	apiClient client.ApiWriter

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

	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
		if err != nil {
			return err
		}
		cli, err := m.LocalClient()
		cli = cli.UseTarget(c.flagTarget)
		c.apiClient = client.NewClient(cli)
		return err
	}
	return cmd
}

// Run handles the enable rgw command.
func (c *cmdEnableRGW) Run(cmd *cobra.Command, args []string) error {
	req := &types.RGWService{
		Port:    c.flagPort,
		Enabled: true,
	}

	err := c.apiClient.EnableRGW(context.Background(), req)
	if err != nil {
		return err
	}

	return nil
}
