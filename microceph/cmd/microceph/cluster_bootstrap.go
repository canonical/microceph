package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/common"
)

type cmdClusterBootstrap struct {
	common  *CmdControl
	cluster *cmdCluster

	flagMonIp      string
	flagPubNet     string
	flagClusterNet string
}

func (c *cmdClusterBootstrap) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Sets up a new cluster",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.flagMonIp, "mon-ip", "", "Public address for bootstrapping ceph mon service.")
	cmd.Flags().StringVar(&c.flagPubNet, "public-network", "", "Public Network for Ceph daemons to bind to.")
	cmd.Flags().StringVar(&c.flagClusterNet, "cluster-network", "", "Cluster Network for Ceph daemons to bind to.")
	return cmd
}

func (c *cmdClusterBootstrap) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
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
	queryCtx, cancel := context.WithTimeout(cmd.Context(), time.Second*30)
	defer cancel()
	err = m.NewCluster(queryCtx, hostname, address, common.EncodeBootstrapConfig(data))
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
