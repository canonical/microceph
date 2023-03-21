package main

import (
	"context"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdDiskAdd struct {
	common    *CmdControl
	disk      *cmdDisk
	apiClient client.ApiWriter

	flagWipe bool
}

func (c *cmdDiskAdd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <PATH>",
		Short: "Add a new Ceph disk (OSD)",
		RunE:  c.Run,
	}

	cmd.PersistentFlags().BoolVar(&c.flagWipe, "wipe", false, "Wipe the disk prior to use")

	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
		if err != nil {
			return err
		}
		cli, err := m.LocalClient()
		c.apiClient = client.NewClient(cli)
		return err
	}

	return cmd
}

func (c *cmdDiskAdd) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	req := &types.DisksPost{
		Path: args[0],
		Wipe: c.flagWipe,
	}

	err := c.apiClient.AddDisk(context.Background(), req)
	if err != nil {
		return err
	}

	return nil
}
