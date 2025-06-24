package main

import (
	"context"
	"encoding/json"
	"fmt"

	microclusterclient "github.com/canonical/microcluster/v2/client"
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
	flagV4MinVersion uint
	flagTarget       string
	flagServiceAddr  string
	client           *microclusterclient.Client
}

func (c *cmdEnableNFS) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nfs --cluster-id <cluster-id> [--v4-min-version 0/1/2] [--target <server>] [--wait <bool>]",
		Short: "Enable the NFS Ganesha service on the --target server (default: this server)",
		RunE:  c.Run,
	}
	cmd.PersistentFlags().StringVar(&c.flagClusterID, "cluster-id", "", "NFS Cluster ID")
	cmd.PersistentFlags().UintVar(&c.flagV4MinVersion, "v4-min-version", 1, "Minimum supported version")
	cmd.PersistentFlags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	cmd.PersistentFlags().StringVar(&c.flagTarget, "service-addr", "0.0.0.0:2049", "Bind IP:PORT for the NFS Ganesha service")
	cmd.Flags().BoolVar(&c.wait, "wait", true, "Wait for nfs service to be up")
	return cmd
}

// Run handles the enable nfs command.
func (c *cmdEnableNFS) Run(cmd *cobra.Command, args []string) error {
	if len(c.flagClusterID) == 0 {
		return fmt.Errorf("please provide a cluster ID using the `--cluster-id` flag")
	}

	if c.flagV4MinVersion > 2 {
		return fmt.Errorf("please provide a valid v4 minimum version (0, 1, 2) using the `--v4-min-version` flag")
	}

	obj := ceph.NFSServicePlacement{
		ClusterID:      c.flagClusterID,
		V4MinVersion:   c.flagV4MinVersion,
		ServiceAddress: c.flagServiceAddr,
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
