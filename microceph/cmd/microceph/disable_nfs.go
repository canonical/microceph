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
	cmd.PersistentFlags().StringVar(&c.flagClusterID, "cluster-id", "", fmt.Sprintf("NFS Cluster ID (must match regex: '%s'", types.NFSClusterIDRegex.String()))
	cmd.PersistentFlags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	return cmd
}

// Run handles the disable nfs command.
func (c *cmdDisableNFS) Run(cmd *cobra.Command, args []string) error {
	if !types.NFSClusterIDRegex.MatchString(c.flagClusterID) {
		return fmt.Errorf("please provide a valid cluster ID using the `--cluster-id` flag (regex: '%s')", types.NFSClusterIDRegex.String())
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
