package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestRemovedSMBHelperCannotRunDaemonStartup(t *testing.T) {
	for _, args := range [][]string{{"smb-native", "compile", "/native/alpha"}, {"smb-native", "ready", "/native/alpha", "smbd"}, {"unknown"}} {
		daemon := cmdDaemon{global: &cmdGlobal{}}
		root := daemon.Command()
		for _, command := range root.Commands() {
			require.NotEqual(t, "smb-native", command.Name())
		}
		started := false
		root.RunE = func(*cobra.Command, []string) error { started = true; return nil }
		root.SilenceUsage, root.SilenceErrors = true, true
		root.SetArgs(args)
		require.Error(t, root.Execute())
		require.False(t, started, "removed helpers must never fall through into daemon startup")
	}
}
