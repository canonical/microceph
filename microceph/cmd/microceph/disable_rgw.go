package main

import (
	"context"

	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

type cmdDisableRGW struct {
	common      *CmdControl
	flagTarget  string
	flagGroupID string
}

func (c *cmdDisableRGW) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rgw [--group-id <group-id>] [--target <server>]",
		Short: "Disable the RGW service on this node",
		RunE:  c.Run,
	}
	cmd.PersistentFlags().StringVar(&c.flagGroupID, "group-id", "", "RGW service group ID (required for grouped RGW instances)")
	cmd.PersistentFlags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	return cmd
}

// Run handles the disable rgw command.
func (c *cmdDisableRGW) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	// If GroupID is provided, use the grouped service delete method
	if c.flagGroupID != "" {
		svc := &types.RGWService{GroupID: c.flagGroupID}
		err = client.DeleteRGWService(context.Background(), cli, c.flagTarget, svc)
		if err != nil {
			return err
		}
	} else {
		// Fall back to ungrouped service delete for backward compatibility
		err = client.DeleteService(context.Background(), cli, c.flagTarget, "rgw")
		if err != nil {
			return err
		}
	}

	return nil
}
