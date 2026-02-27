package main

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/ceph"
)

type cmdWaitready struct {
	common      *CmdControl
	flagTimeout int64
}

func (c *cmdWaitready) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "waitready",
		Short: "Wait until the daemon is ready and Ceph is operational",
		RunE:  c.Run,
	}

	cmd.Flags().Int64Var(&c.flagTimeout, "timeout", 0, "Number of seconds to wait before giving up (0 = indefinitely)")

	return cmd
}

func (c *cmdWaitready) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	ctx := context.Background()
	if c.flagTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(c.flagTimeout)*time.Second)
		defer cancel()
	}

	// Wait for the microcluster daemon to be ready.
	err = m.Ready(ctx)
	if err != nil {
		return fmt.Errorf("daemon not ready: %w", err)
	}

	// Wait for Ceph to be operational (monitor quorum formed).
	err = ceph.WaitForCephReady(ctx)
	if err != nil {
		return fmt.Errorf("ceph not ready: %w", err)
	}

	return nil
}
