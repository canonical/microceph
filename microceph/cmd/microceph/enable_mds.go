package main

import (
	"context"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdEnableMDS struct {
	common     *CmdControl
	wait       bool
	flagTarget string
}

func (c *cmdEnableMDS) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mds [--target <server>] [--wait <bool>]",
		Short: "Enable the MDS service on the --target server (default: this server)",
		RunE:  c.Run,
	}
	cmd.PersistentFlags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	cmd.Flags().BoolVar(&c.wait, "wait", true, "Wait for mds service to be up.")
	return cmd
}

// Run handles the enable mds command.
func (c *cmdEnableMDS) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	req := &types.EnableService{
		Name:    "mds",
		Wait:    c.wait,
		Payload: "",
	}

	err = client.SendServicePlacementReq(context.Background(), cli, req, c.flagTarget)
	if err != nil {
		return err
	}

	return nil
}
