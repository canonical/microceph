package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
)

type cmdClusterJoin struct {
	common  *CmdControl
	cluster *cmdCluster
}

func (c *cmdClusterJoin) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "join <TOKEN>",
		Short: "Joins an existing cluster",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdClusterJoin) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return fmt.Errorf("Unable to configure MicroCluster: %w", err)
	}

	// Get system hostname.
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("Failed to retrieve system hostname: %w", err)
	}

	// Get system address.
	address := util.NetworkInterfaceAddress()
	address = util.CanonicalNetworkAddress(address, 7443)

	token := args[0]
	queryCtx, cancel := context.WithTimeout(cmd.Context(), time.Minute*5)
	defer cancel()
	return m.JoinCluster(queryCtx, hostname, address, token, nil)
}
