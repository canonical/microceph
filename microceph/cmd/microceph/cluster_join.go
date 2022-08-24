package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/canonical/microcluster/microcluster"
	"github.com/lxc/lxd/lxd/util"
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

	m, err := microcluster.App(context.Background(), c.common.FlagStateDir, c.common.FlagLogVerbose, c.common.FlagLogDebug)
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

	return m.JoinCluster(hostname, address, args[0], time.Second*30)
}
