package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdClusterJoin struct {
	common  *CmdControl
	cluster *cmdCluster

	flagMicroCephIp string
}

func (c *cmdClusterJoin) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "join <TOKEN>",
		Short: "Joins an existing cluster",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.flagMicroCephIp, "microceph-ip", "", "Network address microceph daemon binds to.")
	return cmd
}

func (c *cmdClusterJoin) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return fmt.Errorf("unable to configure MicroCluster: %w", err)
	}

	// Get system hostname.
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to retrieve system hostname: %w", err)
	}

	address := c.flagMicroCephIp
	if address == "" {
		// Get system address for microcluster join.
		address = util.NetworkInterfaceAddress()
	}
	address = util.CanonicalNetworkAddress(address, constants.BootstrapPortConst)

	token := args[0]
	return m.JoinCluster(hostname, address, token, nil, time.Minute*5)
}
