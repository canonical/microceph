package main

import (
	"context"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdDiskAdd struct {
	common *CmdControl
	disk   *cmdDisk

	flagWipe    bool
	flagEncrypt bool
	walDevice   string
	dbDevice    string
}

func (c *cmdDiskAdd) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <PATH>",
		Short: "Add a new Ceph disk (OSD)",
		RunE:  c.Run,
	}

	cmd.PersistentFlags().BoolVar(&c.flagWipe, "wipe", false, "Wipe the disk prior to use")
	cmd.PersistentFlags().BoolVar(&c.flagEncrypt, "encrypt", false, "Encrypt the disk prior to use")
	cmd.PersistentFlags().StringVar(&c.walDevice, "wal-device", "", "The device used for WAL")
	cmd.PersistentFlags().StringVar(&c.dbDevice, "db-device", "", "The device used for the DB")

	return cmd
}

func (c *cmdDiskAdd) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	req := &types.DisksPost{
		Path:    args[0],
		Wipe:    c.flagWipe,
		Encrypt: c.flagEncrypt,
	}

	if c.walDevice != "" {
		req.WALDev = &c.walDevice
	}

	if c.dbDevice != "" {
		req.DBDev = &c.dbDevice
	}

	err = client.AddDisk(context.Background(), cli, req)
	if err != nil {
		return err
	}

	return nil
}
