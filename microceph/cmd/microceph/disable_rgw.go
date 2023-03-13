package main

import (
	"context"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdDisableRGW struct {
	common     *CmdControl
	flagTarget string
}

func (c *cmdDisableRGW) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rgw",
		Short: "Disable the RGW service on this node",
		RunE:  c.Run,
	}
	cmd.PersistentFlags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	return cmd
}

// Run handles the disable rgw command.
func (c *cmdDisableRGW) Run(cmd *cobra.Command, args []string) error {

	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}
	cli = cli.UseTarget(c.flagTarget)

	req := &types.RGWService{
		Enabled: false,
	}

	err = client.EnableRGW(context.Background(), cli, req)
	if err != nil {
		return err
	}

	return nil
}
