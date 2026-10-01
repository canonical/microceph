package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"

	"golang.org/x/sys/unix"

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
	common              *CmdControl
	wait                bool
	flagClusterID       string
	flagTarget          string
	flagCredentialsFile string
	flagClustering      string
	flagUserGroupRefs   []string
	flagBindAddresses   []string
	flagBindNetworks    []string
	flagPort            int
}

func (c *cmdEnableSMB) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "smb --cluster-id <cluster-id> [--target <server>] [flags]",
		Short: "Enable a managed SMB service instance on the --target server (default: this server)",
		RunE:  c.Run,
	}
	cmd.Flags().StringVar(&c.flagClusterID, "cluster-id", "", fmt.Sprintf("SMB Cluster ID (must match regex: '%s')", types.SMBClusterIDRegex.String()))
	cmd.Flags().StringVar(&c.flagTarget, "target", "", "Server hostname (default: this server)")
	cmd.Flags().StringVar(&c.flagClustering, "clustering", "", "Clustering mode: always or never (creation default: always; updates inherit)")
	cmd.Flags().StringVar(&c.flagCredentialsFile, "credentials-file", "", "Read local SMB users from a JSON file, or - for stdin (creation only)")
	cmd.Flags().StringArrayVar(&c.flagUserGroupRefs, "user-group-ref", nil, "Reference an existing SMB users-and-groups resource (creation only, repeatable)")
	cmd.Flags().StringArrayVar(&c.flagBindAddresses, "bind-address", nil, "Client-facing IP address for smbd (repeatable)")
	cmd.Flags().StringArrayVar(&c.flagBindNetworks, "bind-network", nil, "Client-facing network from which smbd selects an address (repeatable)")
	cmd.Flags().IntVar(&c.flagPort, "port", 0, "SMB listening port (default 445)")
	cmd.Flags().BoolVar(&c.wait, "wait", true, "Wait for the managed request; initial deployment requires the first share")
	return cmd
}

// Run enables a managed SMB service member.
func (c *cmdEnableSMB) Run(cmd *cobra.Command, _ []string) error {
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
		return fmt.Errorf("please provide port 0 or a valid port number [1, 65535] using the `--port` flag")
	}

	if c.flagCredentialsFile != "" && len(c.flagUserGroupRefs) > 0 {
		return fmt.Errorf("provide either --credentials-file or --user-group-ref, not both")
	}
	var clustering *string
	if c.flagClustering != "" || (cmd != nil && cmd.Flags().Changed("clustering")) {
		if c.flagClustering != "always" && c.flagClustering != "never" {
			return fmt.Errorf("--clustering must be always or never")
		}
		clustering = &c.flagClustering
	}
	var credentials *types.SMBCredentials
	if c.flagCredentialsFile != "" {
		var err error
		credentials, err = readSMBCredentials(c.flagCredentialsFile, cmd.InOrStdin())
		if err != nil {
			return err
		}
	}
	request := &types.ManagedSMBService{
		ClusterID:     c.flagClusterID,
		Credentials:   credentials,
		Clustering:    clustering,
		UserGroupRefs: append([]string(nil), c.flagUserGroupRefs...),
		BindAddresses: append([]string(nil), c.flagBindAddresses...),
		BindNetworks:  append([]string(nil), c.flagBindNetworks...),
		Port:          c.flagPort,
		Wait:          c.wait,
	}
	err := enableManagedSMBServiceFunc(c.common.FlagStateDir, request, c.flagTarget)
	if err != nil {
		return err
	}
	if cmd != nil && (credentials != nil || len(c.flagUserGroupRefs) > 0) {
		if c.wait {
			fmt.Fprintln(cmd.OutOrStdout(), "SMB cluster configured. Create the first CephFS-backed share to deploy SMB instances.")
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "SMB configuration request accepted; check daemon logs for its result. Initial deployment requires the first CephFS-backed share.")
		}
	}
	return nil
}

func readSMBCredentials(path string, input io.Reader) (*types.SMBCredentials, error) {
	if path != "-" {
		// Non-blocking open avoids hanging on FIFOs before we check file type.
		file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
		if err != nil {
			return nil, fmt.Errorf("could not open SMB credentials file")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("SMB credentials input must be a regular file")
		}
		input = file
	}
	const maxSize = 1 << 20
	data, err := io.ReadAll(io.LimitReader(input, maxSize+1))
	if err != nil || len(data) > maxSize {
		return nil, fmt.Errorf("could not read SMB credentials (maximum 1 MiB)")
	}
	var credentials types.SMBCredentials
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&credentials)
	if err != nil {
		return nil, fmt.Errorf("invalid SMB credentials JSON; expected a users array with name and password")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("SMB credentials must contain exactly one JSON object")
	}
	err = credentials.Validate()
	if err != nil {
		return nil, err
	}
	return &credentials, nil
}
