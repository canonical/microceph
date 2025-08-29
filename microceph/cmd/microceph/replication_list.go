package main

import (
	"github.com/spf13/cobra"
)

type cmdReplicationList struct {
	common   *CmdControl
	poolName string
	json     bool
}

func (c *cmdReplicationList) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all resources configured for replication.",
	}

	listRbdCmd := cmdReplicationListRbd{common: c.common}
	cmd.AddCommand(listRbdCmd.Command())

	listCephfsCmd := cmdReplicationListCephfs{common: c.common}
	cmd.AddCommand(listCephfsCmd.Command())

	return cmd
}
