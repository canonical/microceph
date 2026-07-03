package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/microcluster/v3/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
)

type cmdClusterBootstrap struct {
	common  *CmdControl
	cluster *cmdCluster

	flagMicroCephIp      string
	flagMonIp            string
	flagPubNet           string
	flagClusterNet       string
	flagAvailabilityZone string
	flagV2Only           bool
	flagDeferCeph        bool
}

func (c *cmdClusterBootstrap) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Sets up a new cluster",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.flagMicroCephIp, "microceph-ip", "", "Network address microceph daemon binds to.")
	cmd.Flags().StringVar(&c.flagAvailabilityZone, "availability-zone", "", "Availability zone for failure domain distribution.")
	cmd.Flags().StringVar(&c.flagMonIp, "mon-ip", "", "Public address for bootstrapping ceph mon service.")
	cmd.Flags().StringVar(&c.flagPubNet, "public-network", "", "Comma-delimited list of CIDRs for the Ceph public network (Ceph daemons bind addresses).")
	cmd.Flags().StringVar(&c.flagClusterNet, "cluster-network", "", "Comma-delimited list of CIDRs for the Ceph cluster network (OSD replication/recovery traffic).")
	cmd.Flags().BoolVar(&c.flagV2Only, "v2-only", false, "Whether to support V2 messenger only or both V1 and V2")
	cmd.Flags().BoolVar(&c.flagDeferCeph, "defer-ceph", false, "Initialize MicroCluster only and defer Ceph bootstrap. Ceph network flags (--mon-ip, --public-network, --cluster-network, --v2-only) are ignored with this option; pass them to 'cluster bootstrap-ceph' instead.")
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

	address := c.flagMicroCephIp
	if address == "" {
		// Get system address for microcluster bootstrap.
		address = util.NetworkInterfaceAddress()
	}
	address = util.CanonicalNetworkAddress(address, constants.BootstrapPortConst)

	// Set parameter data for Ceph bootstrap.
	data := common.BootstrapConfig{
		MonIp:            c.flagMonIp,
		PublicNet:        c.flagPubNet,
		ClusterNet:       c.flagClusterNet,
		V2Only:           c.flagV2Only,
		AvailabilityZone: c.flagAvailabilityZone,
		DeferCeph:        c.flagDeferCeph,
	}

	if c.flagDeferCeph {
		if c.flagMonIp != "" || c.flagPubNet != "" || c.flagClusterNet != "" || c.flagV2Only {
			return fmt.Errorf("Ceph network flags (--mon-ip, --public-network, --cluster-network, --v2-only) are ignored with --defer-ceph; pass them to 'cluster bootstrap-ceph' instead")
		}
	}

	err = preCheckBootstrapConfig(data)
	if err != nil {
		return err
	}

	// Bootstrap microcluster.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*60)
	defer cancel()

	err = m.Ready(ctx)
	if err != nil {
		return fmt.Errorf("fault while waiting for App readiness: %w", err)
	}

	err = m.NewCluster(ctx, hostname, address, common.EncodeBootstrapConfig(data))
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
