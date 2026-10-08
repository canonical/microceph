package main

import (
	"context"
	"fmt"

	"github.com/canonical/microcluster/v3/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

var disableManagedSMBServiceFunc = func(stateDir string, request *types.ManagedSMBRemoval) error {
	app, err := microcluster.App(microcluster.Args{StateDir: stateDir})
	if err != nil {
		return err
	}
	cli, err := app.LocalClient()
	if err != nil {
		return err
	}
	return client.DisableManagedSMB(context.Background(), cli, request)
}

type cmdDisableSMB struct {
	common        *CmdControl
	flagClusterID string
	flagTarget    string
	flagForce     bool
}

func (c *cmdDisableSMB) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "smb --cluster-id <cluster-id> [--target <server>] [--force]",
		Short: "Remove a managed SMB member, deployment, or logical cluster",
		RunE:  c.Run,
	}
	cmd.Flags().StringVar(&c.flagClusterID, "cluster-id", "", fmt.Sprintf("SMB Cluster ID (must match regex: '%s')", types.SMBClusterIDRegex.String()))
	cmd.Flags().StringVar(&c.flagTarget, "target", "", "Permanently remove this server from the SMB deployment")
	cmd.Flags().BoolVar(&c.flagForce, "force", false, "Delete the logical SMB cluster after upstream share validation")
	return cmd
}

// Run removes the requested managed SMB scope.
func (c *cmdDisableSMB) Run(cmd *cobra.Command, _ []string) error {
	if !types.SMBClusterIDRegex.MatchString(c.flagClusterID) {
		return fmt.Errorf("please provide a valid cluster ID using the `--cluster-id` flag (regex: '%s')", types.SMBClusterIDRegex.String())
	}

	targetWasSet := c.flagTarget != ""
	if cmd != nil {
		targetWasSet = cmd.Flags().Changed("target")
	}
	if targetWasSet && c.flagTarget == "" {
		return fmt.Errorf("--target must name a server")
	}
	if targetWasSet && c.flagForce {
		return fmt.Errorf("--target cannot be used with --force")
	}

	request := &types.ManagedSMBRemoval{
		ClusterID: c.flagClusterID,
		Target:    c.flagTarget,
		Force:     c.flagForce,
	}
	err := request.Validate()
	if err != nil {
		return err
	}
	if cmd != nil {
		switch {
		case request.Target != "":
			fmt.Fprintf(cmd.OutOrStdout(), "Removing SMB member %q from the gateway deployment. The logical SMB cluster, shares, public/private configuration, rank reservations, and CephFS data are preserved.\n", request.Target)
		case request.Force:
			fmt.Fprintf(cmd.OutOrStdout(), "Deleting logical SMB cluster %q and cleaning its deployment. Shares must already be removed; the .smb pool and CephFS data are preserved.\n", request.ClusterID)
		default:
			fmt.Fprintf(cmd.OutOrStdout(), "Removing the entire SMB gateway deployment for %q while preserving the logical SMB cluster, shares, public/private configuration, rank reservations, and CephFS data.\n", request.ClusterID)
		}
	}
	return disableManagedSMBServiceFunc(c.common.FlagStateDir, request)
}
