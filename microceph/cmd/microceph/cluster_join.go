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

type cmdClusterJoin struct {
	common  *CmdControl
	cluster *cmdCluster

	flagMicroCephIp      string
	flagAvailabilityZone string
}

func (c *cmdClusterJoin) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "join <TOKEN>",
		Short: "Joins an existing cluster",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.flagMicroCephIp, "microceph-ip", "", "Network address microceph daemon binds to.")
	cmd.Flags().StringVar(&c.flagAvailabilityZone, "availability-zone", "", "Availability zone for failure domain distribution.")
	return cmd
}

func (c *cmdClusterJoin) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return fmt.Errorf("unable to configure MicroCluster: %w", err)
	}

	// Get system hostname.
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to retrieve system hostname: %w", err)
	}

	token := args[0]

	address := c.flagMicroCephIp
	if address == "" {
		// Pick a local address on the same subnet as a cluster peer,
		// rather than trusting the first global unicast address on the
		// host (canonical/microceph#476).
		var peers []string
		peers, err = common.JoinTokenPeers(token)
		if err != nil {
			return fmt.Errorf("invalid join token: %w", err)
		}

		address, err = common.Network.FindIpForPeers(peers)
		if err != nil {
			return fmt.Errorf("could not auto-detect a local address reachable from cluster peers %v: %w. Pass --microceph-ip explicitly", peers, err)
		}
	}
	address = util.CanonicalNetworkAddress(address, constants.BootstrapPortConst)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()

	joinConfig := common.EncodeJoinConfig(common.JoinConfig{
		AvailabilityZone: c.flagAvailabilityZone,
	})

	return m.JoinCluster(ctx, hostname, address, token, joinConfig)
}
