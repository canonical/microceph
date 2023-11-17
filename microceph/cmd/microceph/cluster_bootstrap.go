package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Rican7/retry"
	"github.com/Rican7/retry/backoff"
	"github.com/Rican7/retry/strategy"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
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

	// Bootstrap microcluster.
	err = m.NewCluster(hostname, address, time.Second*30)
	if err != nil {
		return err
	}

	// Poll microcluster status.
	err = retry.Retry(func(i uint) error {
		_, err = m.Status()
		if err != nil {
			logger.Debugf("microceph status poll attempt #%d", i)
			return err
		}
		return nil
	}, strategy.Delay(1*time.Second), strategy.Limit(10), strategy.Backoff(backoff.Linear(500*time.Millisecond)))
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	// Set parameter data for Ceph bootstrap.
	data := types.Bootstrap{
		MonIp:      c.flagMonIp,
		PublicNet:  c.flagPubNet,
		ClusterNet: c.flagClusterNet,
	}

	err = client.BootstrapCephCluster(context.Background(), cli, &data)
	if err != nil {
		return fmt.Errorf("bootstrap command failed: %w", err)
	}

	return nil
}
