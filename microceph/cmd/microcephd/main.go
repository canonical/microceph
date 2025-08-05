// Package microcephd provides the daemon.
package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/canonical/microcluster/v2/state"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
	"github.com/canonical/microceph/microceph/version"
)

// Debug indicates whether to log debug messages or not.
var Debug bool

// Verbose indicates verbosity.
var Verbose bool

type cmdGlobal struct {
	// cmd *cobra.Command //nolint:structcheck,unused // FIXME: Remove the nolint flag when this is in use.

	flagHelp    bool
	flagVersion bool

	flagLogDebug   bool
	flagLogVerbose bool
}

func (c *cmdGlobal) Run(cmd *cobra.Command, args []string) error {
	return nil
}

type cmdDaemon struct {
	global *cmdGlobal

	flagStateDir string
}

func (c *cmdDaemon) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "microcephd",
		Short:   "Daemon for MicroCeph",
		Version: version.Version(),
	}

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDaemon) Run(cmd *cobra.Command, args []string) error {
	// Initialize the node logger with config file
	pathConst := constants.GetPathConst()
	var err error
	logger.DaemonLogger, err = logger.NewLogger(filepath.Join(pathConst.DataPath, "log-config.json"))
	if err != nil {
		return fmt.Errorf("failed to initialize logging: %w", err)
	}
	logger.Infof("Logger initialized with level: %s", logger.GetLevel())

	m, err := microcluster.App(microcluster.Args{StateDir: c.flagStateDir})
	if err != nil {
		return err
	}

	h := &state.Hooks{}
	h.PostBootstrap = func(ctx context.Context, s state.State, initConfig map[string]string) error {
		data := common.BootstrapConfig{}
		interf := interfaces.CephState{State: s}
		common.DecodeBootstrapConfig(initConfig, &data)
		return ceph.Bootstrap(ctx, interf, data)
	}

	h.PostJoin = func(ctx context.Context, s state.State, initConfig map[string]string) error {
		interf := interfaces.CephState{State: s}
		return ceph.Join(ctx, interf)
	}

	h.OnStart = func(ctx context.Context, s state.State) error {
		interf := interfaces.CephState{State: s}
		return ceph.Start(ctx, interf)
	}

	h.PreRemove = ceph.PreRemove(m)

	daemonArgs := microcluster.DaemonArgs{
		Version: version.Version(),

		Verbose:          c.global.flagLogVerbose,
		Debug:            c.global.flagLogDebug,
		ExtensionsSchema: database.SchemaExtensions,
		APIExtensions:    nil,
		Hooks:            h,
		ExtensionServers: api.Servers,
	}

	return m.Start(context.Background(), daemonArgs)
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

func main() {
	daemonCmd := cmdDaemon{global: &cmdGlobal{}}
	app := daemonCmd.Command()
	app.SilenceUsage = true
	app.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}

	app.PersistentFlags().BoolVarP(&daemonCmd.global.flagHelp, "help", "h", false, "Print help")
	app.PersistentFlags().BoolVar(&daemonCmd.global.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&daemonCmd.global.flagLogDebug, "debug", "d", false, "Show all debug messages")
	app.PersistentFlags().BoolVarP(&daemonCmd.global.flagLogVerbose, "verbose", "v", false, "Show all information messages")

	app.PersistentFlags().StringVar(&daemonCmd.flagStateDir, "state-dir", "", "Path to store state information"+"``")

	app.SetVersionTemplate("{{.Version}}\n")

	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
