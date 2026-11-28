package main

import (
	"encoding/json"

	"github.com/canonical/microcluster/v3/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/client"
)

var getSMBServiceFunc = client.GetSMBService

type cmdStatusSMB struct {
	common *CmdControl
	target string
}

func (c *cmdStatusSMB) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "smb [--target <member>]",
		Short: "Show credential-free native SMB ownership and readiness as JSON",
		Args:  cobra.NoArgs,
		RunE:  c.Run,
	}
	cmd.Flags().StringVar(&c.target, "target", "", "Member hostname (default: local)")
	return cmd
}

// Run observes targeted status without changing configuration or service intent.
func (c *cmdStatusSMB) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}
	cli, err := m.LocalClient()
	if err != nil {
		return err
	}
	status, err := getSMBServiceFunc(cmd.Context(), cli, c.target)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
}
