package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSMBNativeCommandSurface(t *testing.T) {
	enable := (&cmdEnable{common: &CmdControl{}}).Command()
	cmd, _, err := enable.Find([]string{"smb"})
	require.NoError(t, err)
	require.Equal(t, "smb", cmd.Name())
	require.NotNil(t, cmd.Flags().Lookup("cluster-id"))
	require.NotNil(t, cmd.Flags().Lookup("target"))
	require.NotNil(t, cmd.Flags().Lookup("wait"))
	require.Nil(t, cmd.Flags().Lookup("password"))
	require.Nil(t, cmd.Flags().Lookup("public-address"))
	disable := (&cmdDisable{common: &CmdControl{}}).Command()
	cmd, _, err = disable.Find([]string{"smb"})
	require.NoError(t, err)
	require.Equal(t, "smb", cmd.Name())
	require.NotNil(t, cmd.Flags().Lookup("cluster-id"))
	require.NotNil(t, cmd.Flags().Lookup("purge"))
	require.Nil(t, cmd.Flags().Lookup("delete-cluster"))
}
