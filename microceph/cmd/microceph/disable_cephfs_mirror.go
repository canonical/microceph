package main

import (
	"context"

	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/client"
)

type cmdDisableCephFSMirror struct {
	common     *CmdControl
	flagTarget string
}

func (c *cmdDisableCephFSMirror) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cephfs-mirror",
		Short: "Disable the CephFSMirror service",
		RunE:  c.Run,
	}
	cmd.PersistentFlags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	return cmd
}

// Run handles the disable cephfs-mirror command.
func (c *cmdDisableCephFSMirror) Run(cmd *cobra.Command, args []string) error {

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	err = client.DeleteService(context.Background(), cli, c.flagTarget, "cephfs-mirror")
	if err != nil {
		return err
	}

	return nil
}
