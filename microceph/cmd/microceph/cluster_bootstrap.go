package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/canonical/microceph/microceph/constants"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdClusterBootstrap struct {
	common  *CmdControl
	cluster *cmdCluster

	flagMicroCephIp string
	flagMonIp       string
	flagPubNet      string
	flagClusterNet  string
}

func (c *cmdClusterBootstrap) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Sets up a new cluster",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.flagMicroCephIp, "microceph-ip", "", "Public address for microcephd daemon.")
	cmd.Flags().StringVar(&c.flagMonIp, "mon-ip", "", "Public address for bootstrapping ceph mon service.")
	cmd.Flags().StringVar(&c.flagPubNet, "public-network", "", "Public Network for Ceph daemons to bind to.")
	cmd.Flags().StringVar(&c.flagClusterNet, "cluster-network", "", "Cluster Network for Ceph daemons to bind to.")
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

	address := c.flagMicroCephIp
	if address == "" {
		// Get system address for microcluster bootstrap.
		address = util.NetworkInterfaceAddress()
	}
	address = util.CanonicalNetworkAddress(address, constants.BootstrapPortConst)

	// Set parameter data for Ceph bootstrap.
	data := common.BootstrapConfig{
		MonIp:      c.flagMonIp,
		PublicNet:  c.flagPubNet,
		ClusterNet: c.flagClusterNet,
	}

	err = preCheckBootstrapConfig(data)
	if err != nil {
		return err
	}

	// Bootstrap microcluster.
	err = m.NewCluster(hostname, address, common.EncodeBootstrapConfig(data), time.Second*60)
	if err != nil {
		return err
	}

	return nil
}

func preCheckBootstrapConfig(data common.BootstrapConfig) error {
	if len(data.MonIp) != 0 && len(data.PublicNet) != 0 {
		if !common.Network.IsIpOnSubnet(data.MonIp, data.PublicNet) {
			return fmt.Errorf("provided mon-ip %s is not available on provided public network %s", data.MonIp, data.PublicNet)
		}
	}

	return nil
}
