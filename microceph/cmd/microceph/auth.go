package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/canonical/microcluster/v3/microcluster"
	"github.com/spf13/cobra"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/client"
)

var (
	rotateAuthFunc    = client.RotateAuth
	abortAuthFunc     = client.AbortAuth
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
	flagAbort   bool
}

// authRotationBlockedExitCode is the distinct process exit code returned when a
// rotation is blocked (e.g. unmanaged credentials or incompatible client
// sessions), so automation can tell it apart from both success (0) and generic
// failures (1).
const authRotationBlockedExitCode = 3

// authRotationPollInterval is how often the CLI checks auth status while a
// rotation runs detached on the daemon. A variable so tests can speed up
// polling.
var authRotationPollInterval = 2 * time.Second

func (c *cmdAuthRotate) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rotate [--key-type TYPE] [--client NAME]",
		Short: "Rotate CephX authentication keys to a selected key type",
		Long: `Rotate CephX authentication keys to a selected key type.

client.admin is NOT rotated automatically: its key is shared beyond this
cluster — it is distributed through the ceph-conf content interface (e.g. to
MicroCloud's LXD) and often copied to other nodes — and holders that already
loaded it into memory cannot re-authenticate once their ticket expires.
Rotate it explicitly with --client client.admin when you are ready; a full
run pauses before disallowing insecure ciphers until then, and re-running
this command afterwards resumes and finishes.

Use --abort to clear an incomplete rotation (blocked or failed, e.g. a
--client filter whose rotation cannot succeed, or a key type you no longer
want) so new rotations are accepted again. A rotation that is actually
running cannot be aborted. Note that aborting does not undo keys that were
already rotated.

Exits with status 3 when rotation is blocked (e.g. unmanaged credentials,
incompatible client sessions, or a pending admin rotation); a re-run with
the same key type resumes.`,
		RunE: c.Run,
	}
	// Positional arguments are not part of the interface: without this check
	// 'microceph auth rotate aes256k' would silently start a full rotation with
	// the default key type instead of the requested one.
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("unknown argument %q; to select the key type use --key-type", args[0])
		}
		if c.flagAbort && (c.flagKeyType != "" || c.flagClient != "") {
			return fmt.Errorf("--abort cannot be combined with --key-type or --client")
		}
		return nil
	}

	cmd.Flags().StringVar(&c.flagKeyType, "key-type", "", "Key type/cipher to rotate keys to (defaults to auth_preferred_cipher)")
	cmd.Flags().StringVar(&c.flagClient, "client", "", "Rotate and distribute only the specified client key (--client client.admin runs the protected admin rotation, which the full run does not do automatically)")
	cmd.Flags().BoolVar(&c.flagAbort, "abort", false, "Clear an incomplete rotation record so new rotations are accepted again (does not undo keys that were already rotated)")

	return cmd
}

// watchAuthRotation polls auth status until the rotation reaches a terminal
// state. The rotation runs detached on the daemon, so this only tracks it:
// giving up (Ctrl-C, lost daemon) never stops the rotation itself.
func watchAuthRotation(ctx context.Context, poll func() (*types.AuthStatusResponse, error)) (*types.AuthStatusResponse, error) {
	for {
		time.Sleep(authRotationPollInterval)

		status, err := poll()
		if err != nil {
			return nil, fmt.Errorf("lost track of the running rotation (it keeps running in the background; check 'microceph auth status'): %w", err)
		}

		if status.State != "in_progress" {
			return status, nil
		}
	}
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

	if c.flagAbort {
		resp, err := abortAuthFunc(cmd.Context(), cli)
		if err != nil {
			return err
		}

		fmt.Printf("Aborted the %s rotation", resp.State)
		if resp.TargetKeyType != "" {
			fmt.Printf(" to %s", resp.TargetKeyType)
		}
		if resp.ClientName != "" {
			fmt.Printf(" (client: %s)", resp.ClientName)
		}
		fmt.Print("; rotation state cleared\n")
		return nil
	}

	resp, err := rotateAuthFunc(cmd.Context(), cli, c.flagKeyType, c.flagClient)
	if err != nil {
		return err
	}

	// The rotation runs detached on the daemon; poll auth status until it
	// reaches a terminal state so the CLI result reflects the final outcome.
	if resp.State == "in_progress" {
		if c.flagClient == "" {
			fmt.Printf("Rotation started (stage: %s); tracking progress...\n", resp.Stage)
		}

		status, err := watchAuthRotation(cmd.Context(), func() (*types.AuthStatusResponse, error) {
			return getAuthStatusFunc(cmd.Context(), cli)
		})
		if err != nil {
			return err
		}

		resp.State = status.State
		resp.Stage = status.Stage
		resp.Blocker = status.Blocker
		resp.Detail = status.Detail
	}

	if resp.State == "blocked" {
		fmt.Printf("Rotation paused: %s\n", resp.Blocker)
		return &exitCodeError{
			code: authRotationBlockedExitCode,
			err:  fmt.Errorf("auth rotation is blocked: %s", resp.Blocker),
		}
	}

	if resp.State == "failed" {
		return fmt.Errorf("auth rotation failed: %s", resp.Detail)
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
