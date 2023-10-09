package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdDiskRemove struct {
	common *CmdControl
	disk   *cmdDisk

	flagBypassSafety     bool
	flagConfirmDowngrade bool
	flagTimeout          int64
}

func (c *cmdDiskRemove) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <osd-id> [--timeout=300] [--bypass-safety-checks=false] [--confirm-failure-domain-downgrade=false]",
		Short: "Remove a Ceph disk (OSD) given an osd.$id.",
		RunE:  c.Run,
	}

	cmd.PersistentFlags().Int64Var(&c.flagTimeout, "timeout", 1800, "Timeout to wait for safe removal (seconds), default=1800")
	cmd.PersistentFlags().BoolVar(&c.flagBypassSafety, "bypass-safety-checks", false, "Bypass safety checks")
	cmd.PersistentFlags().BoolVar(&c.flagConfirmDowngrade, "confirm-failure-domain-downgrade", false, "Confirm failure domain downgrade if required")

	return cmd
}

func (c *cmdDiskRemove) Run(cmd *cobra.Command, args []string) error {
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

	// parse as int
	osd, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		// check arg is of osd.$id form
		if len(args[0]) < 4 || args[0][:4] != "osd." {
			return fmt.Errorf("Error: osd input must be either in the form $id or osd.$id, got %v", args[0])
		}
		osd, err = strconv.ParseInt(args[0][4:], 10, 64)
		if err != nil {
			return fmt.Errorf("Error: osd input must be either in the form $id or osd.$id: got %v", args[0])
		}
	}

	req := &types.DisksDelete{
		OSD:              osd,
		BypassSafety:     c.flagBypassSafety,
		ConfirmDowngrade: c.flagConfirmDowngrade,
		Timeout:          c.flagTimeout,
	}

	fmt.Printf("Removing osd.%d, timeout %ds\n", osd, req.Timeout)
	err = client.RemoveDisk(context.Background(), cli, req)
	if err != nil {
		return err
	}

	return nil
}
