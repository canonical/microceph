package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/canonical/microcluster/v3/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/client"
)

type cmdSMB struct {
	common    *CmdControl
	disable   bool
	clusterID string
	target    string
	wait      bool
	purge     bool
}

func (c *cmdSMB) Command() *cobra.Command {
	cmd := &cobra.Command{Use: "smb --cluster-id <id> [--target <member>]", Short: "Enable this member's local-user SMB gateway after maintenance", Args: cobra.NoArgs, RunE: c.Run}
	cmd.Flags().StringVar(&c.clusterID, "cluster-id", "", "Upstream logical SMB cluster ID")
	cmd.Flags().StringVar(&c.target, "target", "", "Member hostname (default: local)")
	if c.disable {
		cmd.Short = "Stop and retire this member's SMB gateway; retain logical resources"
		cmd.Flags().BoolVar(&c.purge, "purge", false, "Erase only confirmed-stopped local Samba state")
	} else {
		cmd.Flags().BoolVar(&c.wait, "wait", true, "Wait for local native readiness")
	}
	return cmd
}

// Run invokes only the normal targeted native services API.
func (c *cmdSMB) Run(cmd *cobra.Command, args []string) error {
	data, err := json.Marshal(ceph.SMBServicePlacement{ClusterID: c.clusterID})
	if err != nil {
		return err
	}
	var placement ceph.SMBServicePlacement
	err = placement.PopulateParams(nil, string(data))
	if err != nil {
		return fmt.Errorf("provide a valid --cluster-id: %w", err)
	}
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}
	cli, err := m.LocalClient()
	if err != nil {
		return err
	}
	if c.disable {
		return client.DeleteSMBService(context.Background(), cli, c.target, &types.SMBService{ClusterID: c.clusterID, Purge: c.purge})
	}
	return client.SendServicePlacementReq(context.Background(), cli, &types.EnableService{Name: "smb", Wait: c.wait, Payload: string(data)}, c.target)
}
