package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
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

// authRotationBlockedExitCode is the distinct process exit code returned when a
// rotation is blocked (e.g. unmanaged credentials or incompatible client
// sessions), so automation can tell it apart from both success (0) and generic
// failures (1).
const authRotationBlockedExitCode = 3

func (c *cmdAuthRotate) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rotate [--key-type TYPE] [--client NAME]",
		Short: "Rotate CephX authentication keys to a selected key type",
		Long: `Rotate CephX authentication keys to a selected key type.

Exits with status 3 when rotation is blocked (e.g. unmanaged credentials or
incompatible client sessions); a re-run with the same key type resumes.`,
		RunE: c.Run,
	}
	// Positional arguments are not part of the interface: without this check
	// 'microceph auth rotate aes256k' would silently start a full rotation with
	// the default key type instead of the requested one.
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("unknown argument %q; to select the key type use --key-type", args[0])
		}
		return nil
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
		return &exitCodeError{
			code: authRotationBlockedExitCode,
			err:  fmt.Errorf("auth rotation is blocked: %s", resp.Blocker),
		}
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
		Args:  cobra.NoArgs,
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

	state := resp.State
	if state == "" {
		state = "idle"
	}
	b.WriteString(fmt.Sprintf("State: %s\n", state))
	if resp.Stage != "" {
		b.WriteString(fmt.Sprintf("Stage: %s\n", resp.Stage))
	}
	if resp.TargetKeyType != "" {
		b.WriteString(fmt.Sprintf("Target key type: %s\n", resp.TargetKeyType))
	}
	if resp.Detail != "" {
		b.WriteString(fmt.Sprintf("Detail: %s\n", resp.Detail))
	}

	// Daemon (mon./mgr./osd./mds.) key types; the client summary above covers
	// client entities only.
	if len(resp.ServiceDistribution) > 0 {
		b.WriteString(fmt.Sprintf("Service keys: %s\n", formatCipherCounts(resp.ServiceDistribution, "")))
		writeCipherListing(&b, resp.ServiceDistribution)
	}

	// Client names, one cipher per line when the cluster is mixed: with many
	// clients a single Status line would grow without bound.
	writeCipherListing(&b, resp.ClientDistribution)

	if len(resp.HealthWarnings) > 0 {
		b.WriteString("Warnings:\n")
		for _, warning := range resp.HealthWarnings {
			b.WriteString(fmt.Sprintf("  %s\n", formatAuthWarning(warning)))
		}
	}

	return b.String()
}

// formatCipherCounts summarizes a cipher distribution: "All <cipher>" when
// everything uses one cipher, otherwise per-cipher counts. noun qualifies the
// counts (e.g. "clients"); an empty noun renders bare counts.
func formatCipherCounts(dist map[string][]string, noun string) string {
	ciphers := sortedCipherNames(dist)
	if len(ciphers) == 1 {
		return fmt.Sprintf("All %s", ciphers[0])
	}

	counts := make([]string, 0, len(ciphers))
	for _, cipher := range ciphers {
		if noun != "" {
			counts = append(counts, fmt.Sprintf("%d %s on %s", len(dist[cipher]), noun, cipher))
		} else {
			counts = append(counts, fmt.Sprintf("%d on %s", len(dist[cipher]), cipher))
		}
	}

	return strings.Join(counts, ", ")
}

// writeCipherListing appends one indented "cipher: entity, entity" line per
// cipher, but only when more than one cipher is in use: a uniform distribution
// is fully described by its "All <cipher>" summary line.
func writeCipherListing(b *strings.Builder, dist map[string][]string) {
	if len(dist) <= 1 {
		return
	}

	for _, cipher := range sortedCipherNames(dist) {
		b.WriteString(fmt.Sprintf("  %s: %s\n", cipher, strings.Join(dist[cipher], ", ")))
	}
}

// sortedCipherNames returns the cipher keys of a distribution map in a stable
// (alphabetical) order so status output is deterministic.
func sortedCipherNames(dist map[string][]string) []string {
	names := make([]string, 0, len(dist))
	for cipher := range dist {
		names = append(names, cipher)
	}
	sort.Strings(names)
	return names
}

// formatAuthWarning renders an active AUTH_INSECURE_* health check, adding the
// operator hint for the rotating service keys check (which is expected to clear
// by itself once the old keys expire).
func formatAuthWarning(warning string) string {
	if warning == "AUTH_INSECURE_ROTATING_SERVICE_KEY_TYPE" {
		return warning + " (waiting for old rotating service keys to expire)"
	}

	return warning
}
