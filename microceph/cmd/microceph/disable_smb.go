package main

import (
	"context"
	"fmt"

	"github.com/canonical/microcluster/v3/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

var disableManagedSMBServiceFunc = func(stateDir string, clusterID string, target string) error {
	app, err := microcluster.App(microcluster.Args{StateDir: stateDir})
	if err != nil {
		return err
	}
	cli, err := app.LocalClient()
	if err != nil {
		return err
	}
	return client.DisableManagedSMB(
		context.Background(),
		cli,
		target,
		&types.SMBService{ClusterID: clusterID},
	)
}

type cmdDisableSMB struct {
	common        *CmdControl
	flagClusterID string
	flagTarget    string
}

func (c *cmdDisableSMB) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "smb --cluster-id <cluster-id> [--target <server>]",
		Short: "Disable a managed SMB service instance on the --target server (default: this server)",
		RunE:  c.Run,
	}
	cmd.Flags().StringVar(&c.flagClusterID, "cluster-id", "", fmt.Sprintf("SMB Cluster ID (must match regex: '%s')", types.SMBClusterIDRegex.String()))
	cmd.Flags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	return cmd
}

// Run disables a managed SMB service member.
func (c *cmdDisableSMB) Run(_ *cobra.Command, _ []string) error {
	if !types.SMBClusterIDRegex.MatchString(c.flagClusterID) {
		return fmt.Errorf("please provide a valid cluster ID using the `--cluster-id` flag (regex: '%s')", types.SMBClusterIDRegex.String())
	}
	return disableManagedSMBServiceFunc(c.common.FlagStateDir, c.flagClusterID, c.flagTarget)
}
