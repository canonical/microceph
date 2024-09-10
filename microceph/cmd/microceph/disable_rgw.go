package main

import (
	"context"

	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

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

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	err = client.DeleteService(context.Background(), cli, c.flagTarget, "rgw")
	if err != nil {
		return err
	}

	return nil
}
