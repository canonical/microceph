package main

import (
	"context"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdDisableRGW struct {
	common    *CmdControl
	apiClient client.ApiWriter

	flagTarget string
}

func (c *cmdDisableRGW) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rgw",
		Short: "Disable the RGW service on this node",
		RunE:  c.Run,
	}
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

// Run handles the disable rgw command.
func (c *cmdDisableRGW) Run(cmd *cobra.Command, args []string) error {

	req := &types.RGWService{
		Enabled: false,
	}

	err := c.apiClient.EnableRGW(context.Background(), req)
	if err != nil {
		return err
	}

	return nil
}
