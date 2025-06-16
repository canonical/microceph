package main

import (
	"context"
	"fmt"

	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdDisableNFS struct {
	common        *CmdControl
	flagClusterID string
	flagTarget    string
}

func (c *cmdDisableNFS) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nfs --cluster-id <cluster-id> [--target <server>]",
		Short: "Disable the NFS Ganesha service on the --target server (default: this server)",
		RunE:  c.Run,
	}
	cmd.PersistentFlags().StringVar(&c.flagClusterID, "cluster-id", "", "NFS Cluster ID")
	cmd.PersistentFlags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	return cmd
}

// Run handles the disable nfs command.
func (c *cmdDisableNFS) Run(cmd *cobra.Command, args []string) error {
	if len(c.flagClusterID) == 0 {
		return fmt.Errorf("please provide a cluster ID using the `--cluster-id` flag")
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	svc := &types.NFSService{ClusterID: c.flagClusterID}
	err = client.DeleteNFSService(context.Background(), cli, c.flagTarget, svc)
	if err != nil {
		return err
	}

	return nil
}
