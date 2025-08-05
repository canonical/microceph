package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
)

type cmdEnableNFS struct {
	common           *CmdControl
	wait             bool
	flagClusterID    string
	flagBindAddr     string
	flagBindPort     uint
	flagV4MinVersion uint
	flagTarget       string
}

func (c *cmdEnableNFS) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nfs --cluster-id <cluster-id> [--bind-address <ip-address>] [--port <port-num>] [--v4-min-version 0/1/2] [--target <server>] [--wait <bool>]",
		Short: "Enable the NFS Ganesha service on the --target server (default: this server)",
		RunE:  c.Run,
	}
	cmd.PersistentFlags().StringVar(&c.flagClusterID, "cluster-id", "", fmt.Sprintf("NFS Cluster ID (must match regex: '%s'", types.NFSClusterIDRegex.String()))
	cmd.PersistentFlags().StringVar(&c.flagBindAddr, "bind-address", "0.0.0.0", "Bind address to use for the NFS Ganesha service")
	cmd.PersistentFlags().UintVar(&c.flagBindPort, "bind-port", 2049, "Bind port to use for the NFS Ganesha service")
	cmd.PersistentFlags().UintVar(&c.flagV4MinVersion, "v4-min-version", 1, "Minimum supported version")
	cmd.PersistentFlags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	cmd.Flags().BoolVar(&c.wait, "wait", true, "Wait for nfs service to be up")
	return cmd
}

// Run handles the enable nfs command.
func (c *cmdEnableNFS) Run(cmd *cobra.Command, args []string) error {
	if !types.NFSClusterIDRegex.MatchString(c.flagClusterID) {
		return fmt.Errorf("please provide a valid cluster ID using the `--cluster-id` flag (regex: '%s')", types.NFSClusterIDRegex.String())
	}

	if c.flagV4MinVersion > 2 {
		return fmt.Errorf("please provide a valid v4 minimum version (0, 1, 2) using the `--v4-min-version` flag")
	}

	ip := net.ParseIP(c.flagBindAddr)
	if ip == nil {
		return fmt.Errorf("could not parse the given `--bind-address`")
	}

	// 49152 - 65535 - dynamic and / or private ports.
	if c.flagBindPort == 0 || c.flagBindPort > 49151 {
		return fmt.Errorf("please provide a valid port number [1, 49151] using the `--bind-port` flag")
	}

	obj := ceph.NFSServicePlacement{
		ClusterID:    c.flagClusterID,
		V4MinVersion: c.flagV4MinVersion,
		BindAddress:  c.flagBindAddr,
		BindPort:     c.flagBindPort,
	}
	jsp, err := json.Marshal(obj)
	if err != nil {
		return err
	}

	req := &types.EnableService{
		Name:    "nfs",
		Wait:    c.wait,
		Payload: string(jsp[:]),
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	return client.SendServicePlacementReq(context.Background(), cli, req, c.flagTarget)
}
