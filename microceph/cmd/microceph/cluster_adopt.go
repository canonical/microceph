package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
)

type cmdClusterAdopt struct {
	common  *CmdControl
	cluster *cmdCluster

	flagMicrocephIP string
	flagFSID        string
	flagMonHosts    []string
	flagPubNet      string
	flagClusterNet  string
}

func (c *cmdClusterAdopt) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "adopt [Admin Key File]",
		Short: "adopt an existing ceph cluster",
		Long:  "Reads provided file containing admin key or stdin if no input or '-' is provided.",
		Args:  cobra.ExactArgs(1),
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.flagMicrocephIP, "microceph-ip", "", "Ceph cluster fsid")
	cmd.Flags().StringVar(&c.flagFSID, "fsid", "", "Ceph cluster fsid")
	cmd.Flags().StringSliceVar(&c.flagMonHosts, "mon-hosts", []string{}, "Comma separated list of mon addresses")
	cmd.Flags().StringVar(&c.flagPubNet, "public-network", "", "Public network Ceph daemons bind to")
	cmd.Flags().StringVar(&c.flagClusterNet, "cluster-network", "", "Cluster network Ceph daemons bind to")
	return cmd
}

func (c *cmdClusterAdopt) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return fmt.Errorf("unable to configure MicroCeph: %w", err)
	}

	// Get system hostname.
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to retrieve system hostname: %w", err)
	}

	// populate default microceph address if not provided.
	populateMicroCephAddress(&c.flagMicrocephIP)
	adminKey, err := c.fetchAdminKey(cmd, args)
	if err != nil || len(adminKey) == 0 {
		logger.Errorf("failed to fetch admin key (%s): %v", adminKey, err)
		return err
	}

	// Set parameter data for Ceph bootstrap.
	data := common.BootstrapConfig{
		PublicNet:     c.flagPubNet,
		ClusterNet:    c.flagClusterNet,
		AdoptFSID:     c.flagFSID,
		AdoptMonHosts: c.flagMonHosts,
		AdoptAdminKey: adminKey,
	}

	err = c.preCheckAdoptConfig(data)
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

	err = m.NewCluster(ctx, hostname, c.flagMicrocephIP, common.EncodeBootstrapConfig(data))
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdClusterAdopt) fetchAdminKey(cmd *cobra.Command, args []string) (string, error) {
	inputReader := cmd.InOrStdin()

	if len(args) > 0 && args[0] != "-" {
		file, err := os.Open(args[0])
		if err != nil {
			logger.Errorf("failed to open admin key file: %v", err)
			return "", err
		}
		// over-write inputReader if file path is provided.
		inputReader = file
	}

	adminkey, err := io.ReadAll(inputReader)
	if err != nil {
		logger.Errorf("failed to read admin key: %v", err)
		return "", err
	}

	key := strings.TrimSpace(string(adminkey))

	return key, nil
}

func (c *cmdClusterAdopt) preCheckAdoptConfig(data common.BootstrapConfig) error {
	if len(data.AdoptFSID) == 0 {
		return fmt.Errorf("missing fsid is mandatory for adopting a ceph cluster")
	}

	if len(data.AdoptAdminKey) == 0 {
		return fmt.Errorf("cannot adopt a ceph cluster without admin key")
	}

	if len(data.AdoptMonHosts) == 0 {
		return fmt.Errorf("missing mon hosts mandatory for adopting a ceph cluster")
	}

	return nil
}

func populateMicroCephAddress(microcephIP *string) {
	if len(*microcephIP) == 0 {
		// Get system address for microcluster bootstrap.
		*microcephIP = util.NetworkInterfaceAddress()
	}

	*microcephIP = util.CanonicalNetworkAddress(*microcephIP, constants.BootstrapPortConst)
}
