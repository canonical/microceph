package main

import (
	"context"
	"fmt"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/client"
)

type cmdClusterMaintenanceExit struct {
	common *CmdControl

	flagDryRun      bool
	flagCheckOnly   bool
	flagIgnoreCheck bool
}

func (c *cmdClusterMaintenanceExit) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exit <NODE_NAME>",
		Short: "Exit maintenance mode.",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.flagDryRun, "dry-run", false, "Dry run the command.")
	cmd.Flags().BoolVar(&c.flagCheckOnly, "check-only", false, "Only run the preflight checks (mutually exclusive with --ignore-check).")
	cmd.Flags().BoolVar(&c.flagIgnoreCheck, "ignore-check", false, "Ignore the the preflight checks (mutually exclusive with --check-only).")
	cmd.MarkFlagsMutuallyExclusive("check-only", "ignore-check")

	return cmd
}

func (c *cmdClusterMaintenanceExit) Run(cmd *cobra.Command, args []string) error {
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

	results, err := client.ExitMaintenance(context.Background(), cli, args[0], c.flagDryRun, c.flagCheckOnly, c.flagIgnoreCheck)
	if err != nil {
		return fmt.Errorf("failed to exit maintenance mode: %v", err)
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
