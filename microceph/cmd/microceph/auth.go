package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/canonical/microcluster/v3/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

var (
	rotateAuthFunc    = client.RotateAuth
	getAuthStatusFunc = client.GetAuthStatus
)

type cmdAuth struct {
	common *CmdControl
}

func (c *cmdAuth) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage MicroCeph CephX authentication keys",
	}

	authRotateCmd := cmdAuthRotate{common: c.common, auth: c}
	cmd.AddCommand(authRotateCmd.Command())

	authStatusCmd := cmdAuthStatus{common: c.common, auth: c}
	cmd.AddCommand(authStatusCmd.Command())

	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}

type cmdAuthRotate struct {
	common *CmdControl
	auth   *cmdAuth

	flagKeyType string
	flagClient  string
}

func (c *cmdAuthRotate) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rotate [--key-type TYPE] [--client NAME]",
		Short: "Rotate CephX authentication keys to a selected key type",
		RunE:  c.Run,
	}

	cmd.Flags().StringVar(&c.flagKeyType, "key-type", "", "Key type/cipher to rotate keys to (defaults to auth_preferred_cipher)")
	cmd.Flags().StringVar(&c.flagClient, "client", "", "Rotate and distribute only the specified client key")

	return cmd
}

func (c *cmdAuthRotate) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	resp, err := rotateAuthFunc(cmd.Context(), cli, c.flagKeyType, c.flagClient)
	if err != nil {
		return err
	}

	if resp.State == "blocked" {
		fmt.Printf("Rotation paused: %s\n", resp.Blocker)
		return nil
	}

	if c.flagClient != "" {
		fmt.Printf("Successfully rotated key for %s\n", resp.ClientName)
		return nil
	}

	fmt.Printf("Successfully completed auth key rotation to %s\n", resp.TargetKeyType)
	return nil
}

type cmdAuthStatus struct {
	common *CmdControl
	auth   *cmdAuth

	flagJSON bool
}

func (c *cmdAuthStatus) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [--json]",
		Short: "Display CephX key rotation status and blockers",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.flagJSON, "json", false, "Output status as JSON")

	return cmd
}

func (c *cmdAuthStatus) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	resp, err := getAuthStatusFunc(cmd.Context(), cli)
	if err != nil {
		return err
	}

	if c.flagJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	fmt.Print(formatAuthStatusText(resp))
	return nil
}

func formatAuthStatusText(resp *types.AuthStatusResponse) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Status: %s\n", resp.Status))
	if resp.Blocker != "" {
		b.WriteString(fmt.Sprintf("Blocker: %s\n", resp.Blocker))
	}
	return b.String()
}
