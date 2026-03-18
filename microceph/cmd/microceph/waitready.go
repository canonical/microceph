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
	flagTimeout uint64
	flagStorage bool
}

func (c *cmdWaitready) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "waitready",
		Short: "Wait until the daemon is ready and Ceph is operational",
		RunE:  c.Run,
	}

	cmd.Flags().Uint64Var(&c.flagTimeout, "timeout", 0, "Number of seconds to wait before giving up (0 = indefinitely)")
	cmd.Flags().BoolVar(&c.flagStorage, "storage", false, "Wait until enough OSDs are up to satisfy pool replication requirements")
	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return fmt.Errorf("%w: timeout must be a positive number of seconds or zero", err)
	})

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

	// Optionally wait for enough OSDs to satisfy pool replication.
	if c.flagStorage {
		err = ceph.WaitForOSDsReady(ctx)
		if err != nil {
			return fmt.Errorf("storage not ready: %w", err)
		}
	}

	return nil
}
