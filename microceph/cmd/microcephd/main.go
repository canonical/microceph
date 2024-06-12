// Package microcephd provides the daemon.
package main

import (
	"context"
	"github.com/canonical/microceph/microceph/interfaces"
	"math/rand"
	"os"
	"time"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microcluster/config"
	"github.com/canonical/microcluster/microcluster"
	"github.com/canonical/microcluster/state"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
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
	Debug = c.flagLogDebug
	Verbose = c.flagLogVerbose

	return logger.InitLogger("", "", c.flagLogVerbose, c.flagLogDebug, nil)
}

type cmdDaemon struct {
	global *cmdGlobal

	flagStateDir string
}

func (c *cmdDaemon) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "microcephd",
		Short:   "Daemon for MicroCeph",
		Version: version.Version,
	}

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDaemon) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(microcluster.Args{StateDir: c.flagStateDir, Verbose: c.global.flagLogVerbose, Debug: c.global.flagLogDebug})
	if err != nil {
		return err
	}

	h := &config.Hooks{}
	h.PostBootstrap = func(s *state.State, initConfig map[string]string) error {
		data := common.BootstrapConfig{}
		interf := interfaces.CephState{State: s}
		common.DecodeBootstrapConfig(initConfig, &data)
		return ceph.Bootstrap(interf, data)
	}

	h.PostJoin = func(s *state.State, initConfig map[string]string) error {
		interf := interfaces.CephState{State: s}
		return ceph.Join(interf)
	}

	h.OnStart = func(s *state.State) error {
		interf := interfaces.CephState{State: s}
		return ceph.Start(interf)
	}

	return m.Start(context.Background(), api.Endpoints, database.SchemaExtensions, nil, h)
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
	app.PersistentFlags().BoolVarP(&daemonCmd.global.flagLogDebug, "debug", "d", true, "Show all debug messages")
	app.PersistentFlags().BoolVarP(&daemonCmd.global.flagLogVerbose, "verbose", "v", false, "Show all information messages")

	app.PersistentFlags().StringVar(&daemonCmd.flagStateDir, "state-dir", "", "Path to store state information"+"``")

	app.SetVersionTemplate("{{.Version}}\n")

	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
