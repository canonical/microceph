package main

import (
	"context"
	"fmt"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdLog struct {
	common *CmdControl
}

type cmdLogSetLevel struct {
	common      *CmdControl
	logSetLevel *cmdLog
	logLevel    string
}

type cmdLogGetLevel struct {
	common      *CmdControl
	logGetLevel *cmdLog
}

func (c *cmdLogSetLevel) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-level <LEVEL>",
		Short: "Set the log level for the microceph daemon",
		Long: `Set the log level for the microceph daemon.
    LEVEL is either a symbolic string, or an integer that
    specifies the new level. The mapping is as follows:
    0 - PANIC
    1 - FATAL
    2 - ERROR
    3 - WARNING
    4 - INFO
    5 - DEBUG
    6 - TRACE.`,
		RunE: c.Run,
	}

	return cmd
}

func (c *cmdLogGetLevel) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get-level",
		Short: "Get the current log level, as an integer",
        RunE: c.Run,
	}

	return cmd
}

func (c *cmdLogSetLevel) Run(cmd *cobra.Command, args []string) error {
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

	req := &types.LogLevelPut{
		Level: args[0],
	}

	return client.LogLevelSet(context.Background(), cli, req)
}

func (c *cmdLogGetLevel) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
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

	lvl, err := client.LogLevelGet(context.Background(), cli)
	if err != nil {
		return err
	}

	fmt.Printf("%d\n", lvl)
	return nil
}

func (c *cmdLog) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Manage microceph logs",
	}

	// set-level.
	logLevelSetCmd := cmdLogSetLevel{common: c.common, logSetLevel: c}
	cmd.AddCommand(logLevelSetCmd.Command())

	// get-level.
	logLevelGetCmd := cmdLogGetLevel{common: c.common, logGetLevel: c}
	cmd.AddCommand(logLevelGetCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}
