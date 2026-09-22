package main

import (
	"context"
	"fmt"
	"net"

	"github.com/canonical/microcluster/v3/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

var enableManagedSMBServiceFunc = func(stateDir string, request *types.ManagedSMBService, target string) error {
	app, err := microcluster.App(microcluster.Args{StateDir: stateDir})
	if err != nil {
		return err
	}
	cli, err := app.LocalClient()
	if err != nil {
		return err
	}
	return client.EnableManagedSMB(context.Background(), cli, target, request)
}

type cmdEnableSMB struct {
	common             *CmdControl
	wait               bool
	flagClusterID      string
	flagTarget         string
	flagDefineUserPass []string
	flagUserGroupRefs  []string
	flagBindAddresses  []string
	flagBindNetworks   []string
	flagPort           int
}

func (c *cmdEnableSMB) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "smb --cluster-id <cluster-id> [--target <server>] [flags]",
		Short: "Enable a managed SMB service instance on the --target server (default: this server)",
		RunE:  c.Run,
	}
	cmd.Flags().StringVar(&c.flagClusterID, "cluster-id", "", fmt.Sprintf("SMB Cluster ID (must match regex: '%s')", types.SMBClusterIDRegex.String()))
	cmd.Flags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	cmd.Flags().StringArrayVar(&c.flagDefineUserPass, "define-user-pass", nil, "Define a local SMB user as username%password (creation only, repeatable)")
	cmd.Flags().StringArrayVar(&c.flagUserGroupRefs, "user-group-ref", nil, "Reference an existing SMB users-and-groups resource (creation only, repeatable)")
	cmd.Flags().StringArrayVar(&c.flagBindAddresses, "bind-address", nil, "Client-facing IP address for smbd (repeatable)")
	cmd.Flags().StringArrayVar(&c.flagBindNetworks, "bind-network", nil, "Client-facing network from which smbd selects an address (repeatable)")
	cmd.Flags().IntVar(&c.flagPort, "port", 0, "SMB listening port (default 445)")
	cmd.Flags().BoolVar(&c.wait, "wait", true, "Wait for the SMB service instance to be ready")
	return cmd
}

// Run enables a managed SMB service member.
func (c *cmdEnableSMB) Run(_ *cobra.Command, _ []string) error {
	if !types.SMBClusterIDRegex.MatchString(c.flagClusterID) {
		return fmt.Errorf("please provide a valid cluster ID using the `--cluster-id` flag (regex: '%s')", types.SMBClusterIDRegex.String())
	}
	if len(c.flagBindAddresses) > 0 && len(c.flagBindNetworks) > 0 {
		return fmt.Errorf("provide either `--bind-address` or `--bind-network`, not both")
	}
	for _, address := range c.flagBindAddresses {
		if net.ParseIP(address) == nil {
			return fmt.Errorf("could not parse the given `--bind-address`")
		}
	}
	for _, network := range c.flagBindNetworks {
		_, _, err := net.ParseCIDR(network)
		if err != nil {
			return fmt.Errorf("could not parse the given `--bind-network`")
		}
	}
	if c.flagPort < 0 || c.flagPort > 65535 {
		return fmt.Errorf("please provide a valid port number [1, 65535] using the `--port` flag")
	}

	request := &types.ManagedSMBService{
		ClusterID:      c.flagClusterID,
		DefineUserPass: append([]string(nil), c.flagDefineUserPass...),
		UserGroupRefs:  append([]string(nil), c.flagUserGroupRefs...),
		BindAddresses:  append([]string(nil), c.flagBindAddresses...),
		BindNetworks:   append([]string(nil), c.flagBindNetworks...),
		Port:           c.flagPort,
		Wait:           c.wait,
	}
	return enableManagedSMBServiceFunc(c.common.FlagStateDir, request, c.flagTarget)
}
