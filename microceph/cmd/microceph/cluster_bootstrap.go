package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdClusterBootstrap struct {
	common  *CmdControl
	cluster *cmdCluster

	flagMonIp string
}

func (c *cmdClusterBootstrap) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Sets up a new cluster",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.flagMonIp, "mon-ip", "", "Public address for bootstrapping ceph mon service.")
	return cmd
}

func (c *cmdClusterBootstrap) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return cmd.Help()
	}

	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return fmt.Errorf("unable to configure MicroCeph: %w", err)
	}

	// Get system hostname.
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to retrieve system hostname: %w", err)
	}

	// Get system address for microcluster bootstrap.
	address := util.NetworkInterfaceAddress()
	address = util.CanonicalNetworkAddress(address, common.BootstrapPortConst)

	return m.NewCluster(hostname, address, time.Minute*5)
	// Get parameter data for Ceph bootstrap.
	var e error
	var data types.Bootstrap
	if len(c.flagMonIp) > 0 {
		data, e = getCephBootstrapData(c.flagMonIp)
	} else {
		data, e = getCephBootstrapData(util.NetworkInterfaceAddress())
	}
	if e != nil {
		return fmt.Errorf("failed to parse bootstrap data: %w", e)
	}

	// Bootstrap microceph daemon.
	err = m.NewCluster(hostname, address, time.Second*30)
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	err = client.BootstrapCephCluster(context.Background(), cli, &data)
	if err != nil {
		return fmt.Errorf("bootstrap command failed: %w", err)
	}

	return nil
}

func getCephBootstrapData(monip string) (types.Bootstrap, error) {
	cephPublicNetwork, err := common.Network.FindNetworkAddress(monip)
	if err != nil {
		return types.Bootstrap{}, fmt.Errorf("failed to locate %s on host: %w", monip, err)
	}

	return types.Bootstrap{MonIp: monip, PubNet: cephPublicNetwork}, nil
}
