package main

import (
	"context"
	"fmt"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/client"
)

type cmdClusterMaintenanceEnter struct {
	common *CmdControl

	flagForce       bool
	flagDryRun      bool
	flagSetNoout    bool
	flagStopOsds    bool
	flagCheckOnly   bool
	flagIgnoreCheck bool
}

func (c *cmdClusterMaintenanceEnter) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enter <NODE_NAME>",
		Short: "Enter maintenance mode.",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.flagForce, "force", false, "Force to enter maintenance mode.")
	cmd.Flags().BoolVar(&c.flagDryRun, "dry-run", false, "Dry run the command.")
	cmd.Flags().BoolVar(&c.flagSetNoout, "set-noout", true, "Stop CRUSH from rebalancing the cluster.")
	cmd.Flags().BoolVar(&c.flagStopOsds, "stop-osds", false, "Stop the OSDS when entering maintenance mode.")
	cmd.Flags().BoolVar(&c.flagCheckOnly, "check-only", false, "Only run the preflight checks (mutually exclusive with --ignore-check).")
	cmd.Flags().BoolVar(&c.flagIgnoreCheck, "ignore-check", false, "Ignore the the preflight checks (mutually exclusive with --check-only).")
	cmd.MarkFlagsMutuallyExclusive("check-only", "ignore-check")
	return cmd
}

func (c *cmdClusterMaintenanceEnter) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	results, err := client.EnterMaintenance(context.Background(), cli, args[0], c.flagForce, c.flagDryRun, c.flagSetNoout, c.flagStopOsds, c.flagCheckOnly, c.flagIgnoreCheck)
	if err != nil && !c.flagForce {
		return fmt.Errorf("failed to enter maintenance mode: %v", err)
	}

	for _, result := range results {
		if c.flagDryRun {
			fmt.Println(result.Action)
		} else {
			errMessage := result.Error
			if errMessage == "" {
				fmt.Printf("%s (succeeded)\n", result.Action)
			} else {
				fmt.Printf("%s (failed: %s)\n", result.Action, errMessage)
			}
		}
	}

	return nil
}
