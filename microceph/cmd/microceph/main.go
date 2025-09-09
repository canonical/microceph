// Package microceph provides the main client tool.
package main

import (
	"bufio"
	"os"

	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/microceph/microceph/clilogger"
	"github.com/canonical/microceph/microceph/version"
	"github.com/spf13/cobra"
)

// CmdControl has functions that are common to the microctl commands.
// command line tools.
type CmdControl struct {
	cmd *cobra.Command //nolint:structcheck,unused // FIXME: Remove the nolint flag when this is in use.

	Asker          cli.Asker // Asker object for prompts on CLI.
	FlagHelp       bool
	FlagVersion    bool
	FlagLogDebug   bool
	FlagLogVerbose bool
	FlagStateDir   string
}

func main() {
	// common flags. Not using a logger at this time.
	commonCmd := CmdControl{Asker: cli.NewAsker(bufio.NewReader(os.Stdin), nil)}

	app := &cobra.Command{
		Use:               "microceph",
		Short:             "Command for managing the MicroCeph deployment",
		Version:           version.Version(),
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}

	app.PersistentFlags().StringVar(&commonCmd.FlagStateDir, "state-dir", "", "Path to store state information"+"``")
	app.PersistentFlags().BoolVarP(&commonCmd.FlagHelp, "help", "h", false, "Print help")
	app.PersistentFlags().BoolVar(&commonCmd.FlagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&commonCmd.FlagLogDebug, "debug", "d", false, "Show all debug messages")
	app.PersistentFlags().BoolVarP(&commonCmd.FlagLogVerbose, "verbose", "v", false, "Show all information messages")

	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version()

	// Initialize CLI logger based on flags
	app.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		clilogger.InitLogger(commonCmd.FlagLogDebug, commonCmd.FlagLogVerbose)
	}

	// Top-level.
	cmdEnable := cmdEnable{common: &commonCmd}
	app.AddCommand(cmdEnable.Command())

	cmdDisable := cmdDisable{common: &commonCmd}
	app.AddCommand(cmdDisable.Command())

	cmdInit := cmdInit{common: &commonCmd}
	app.AddCommand(cmdInit.Command())

	cmdStatus := cmdStatus{common: &commonCmd}
	app.AddCommand(cmdStatus.Command())

	// Nested.
	cmdCluster := cmdCluster{common: &commonCmd}
	app.AddCommand(cmdCluster.Command())

	cmdRemote := cmdRemote{common: &commonCmd}
	app.AddCommand(cmdRemote.Command())

	// Replication command
	cmdReplication := cmdReplication{common: &commonCmd}
	app.AddCommand(cmdReplication.Command())

	cmdDisk := cmdDisk{common: &commonCmd}
	app.AddCommand(cmdDisk.Command())

	cmdClient := cmdClient{common: &commonCmd}
	app.AddCommand(cmdClient.Command())

	cmdPool := cmdPool{common: &commonCmd}
	app.AddCommand(cmdPool.Command())

	cmdLog := cmdLog{common: &commonCmd}
	app.AddCommand(cmdLog.Command())

	app.InitDefaultHelpCmd()

	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
