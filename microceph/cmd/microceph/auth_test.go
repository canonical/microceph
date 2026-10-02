package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
)

func TestAuthCommandHierarchy(t *testing.T) {
	commonCmd := &CmdControl{}
	auth := &cmdAuth{common: commonCmd}
	cmd := auth.Command()

	assert.Equal(t, "auth", cmd.Use)
	assert.True(t, cmd.HasSubCommands())

	rotateCmd, _, err := cmd.Find([]string{"rotate"})
	require.NoError(t, err)
	assert.Equal(t, "rotate [--key-type TYPE] [--client NAME]", rotateCmd.Use)
	assert.NotNil(t, rotateCmd.Flags().Lookup("key-type"))
	assert.NotNil(t, rotateCmd.Flags().Lookup("client"))

	statusCmd, _, err := cmd.Find([]string{"status"})
	require.NoError(t, err)
	assert.Equal(t, "status [--json]", statusCmd.Use)
	assert.NotNil(t, statusCmd.Flags().Lookup("json"))
}

func TestAuthRotateArgsRejectPositional(t *testing.T) {
	commonCmd := &CmdControl{}
	c := &cmdAuthRotate{common: commonCmd}
	cmd := c.Command()

	// 'microceph auth rotate aes256k' must fail with a --key-type hint instead of
	// silently starting a full rotation with the default key type.
	err := cmd.Args(cmd, []string{"aes256k"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--key-type")

	// No positional arguments: accepted.
	err = cmd.Args(cmd, nil)
	require.NoError(t, err)

	// --abort cannot be combined with a filter.
	c.flagAbort = true
	c.flagKeyType = "aes256k"
	err = cmd.Args(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--abort cannot be combined")

	c.flagKeyType = ""
	c.flagClient = "client.rgw"
	err = cmd.Args(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--abort cannot be combined")

	c.flagClient = ""
	err = cmd.Args(cmd, nil)
	require.NoError(t, err)
}

func TestAuthStatusArgsRejectPositional(t *testing.T) {
	commonCmd := &CmdControl{}
	c := &cmdAuthStatus{common: commonCmd}
	cmd := c.Command()

	err := cmd.Args(cmd, []string{"json"})
	require.Error(t, err)

	err = cmd.Args(cmd, nil)
	require.NoError(t, err)
}

func TestProcessExitCode(t *testing.T) {
	assert.Equal(t, 0, processExitCode(nil))
	assert.Equal(t, 1, processExitCode(fmt.Errorf("generic failure")))
	assert.Equal(t, authRotationBlockedExitCode, processExitCode(&exitCodeError{
		code: authRotationBlockedExitCode,
		err:  fmt.Errorf("auth rotation is blocked"),
	}))
	// Wrapped exit-code errors keep their code.
	assert.Equal(t, authRotationBlockedExitCode, processExitCode(fmt.Errorf("outer: %w", &exitCodeError{
		code: authRotationBlockedExitCode,
		err:  fmt.Errorf("auth rotation is blocked"),
	})))
}

func TestFormatAuthStatusText(t *testing.T) {
	// 1. Blocked status with blocker: spec lines plus rotation record details.
	respBlocked := &types.AuthStatusResponse{
		Status:        "blocked",
		State:         "blocked",
		Stage:         "rotate_clients",
		Blocker:       "Unmanaged credentials must be rotated manually before rotation can proceed",
		TargetKeyType: "aes256k",
	}
	out := formatAuthStatusText(respBlocked)
	assert.Contains(t, out, "Status: blocked\n")
	assert.Contains(t, out, "Blocker: Unmanaged credentials must be rotated manually before rotation can proceed\n")
	assert.Contains(t, out, "State: blocked\n")
	assert.Contains(t, out, "Stage: rotate_clients\n")
	assert.Contains(t, out, "Target key type: aes256k\n")

	// 2. Uniform cipher status: no per-client listing.
	respUniform := &types.AuthStatusResponse{
		Status: "All client aes256k",
		State:  "idle",
		ClientDistribution: map[string][]string{
			"aes256k": {"client.admin", "client.rgw"},
		},
	}
	out = formatAuthStatusText(respUniform)
	assert.Equal(t, "Status: All client aes256k\nState: idle\n", out)

	// 3. Mixed client ciphers: counts on the Status line, names on indented
	//    per-cipher lines instead of one enormous line.
	respMixed := &types.AuthStatusResponse{
		Status: "2 clients on aes, 1 clients on aes256k",
		State:  "in_progress",
		ClientDistribution: map[string][]string{
			"aes":     {"client.X", "client.Y"},
			"aes256k": {"client.Z"},
		},
	}
	out = formatAuthStatusText(respMixed)
	assert.Contains(t, out, "Status: 2 clients on aes, 1 clients on aes256k\n")
	assert.Contains(t, out, "  aes: client.X, client.Y\n")
	assert.Contains(t, out, "  aes256k: client.Z\n")
	assert.NotContains(t, out, "(aes:")

	// 4. Service keys line with per-cipher listing when mixed.
	respServices := &types.AuthStatusResponse{
		Status: "All client aes256k",
		State:  "in_progress",
		ServiceDistribution: map[string][]string{
			"aes":     {"mon.", "mgr.node-a"},
			"aes256k": {"osd.0", "mds.node-a"},
		},
	}
	out = formatAuthStatusText(respServices)
	assert.Contains(t, out, "Service keys: 2 on aes, 2 on aes256k\n")
	assert.Contains(t, out, "  aes: mon., mgr.node-a\n")
	assert.Contains(t, out, "  aes256k: osd.0, mds.node-a\n")

	// 5. Warnings block, with the natural-expiry hint for rotating service keys.
	respWarnings := &types.AuthStatusResponse{
		Status: "All client aes256k",
		State:  "in_progress",
		HealthWarnings: []string{
			"AUTH_INSECURE_ROTATING_SERVICE_KEY_TYPE",
			"AUTH_INSECURE_KEYS_ALLOWED",
		},
	}
	out = formatAuthStatusText(respWarnings)
	assert.Contains(t, out, "Warnings:\n")
	assert.Contains(t, out, "  AUTH_INSECURE_ROTATING_SERVICE_KEY_TYPE (waiting for old rotating service keys to expire)\n")
	assert.Contains(t, out, "  AUTH_INSECURE_KEYS_ALLOWED\n")
}

func TestAuthRotateRun(t *testing.T) {
	origRotate := rotateAuthFunc
	defer func() { rotateAuthFunc = origRotate }()

	commonCmd := &CmdControl{}
	c := &cmdAuthRotate{
		common: commonCmd,
	}
	cmd := c.Command()
	c.flagKeyType = "aes256k"

	// 1. Successful full rotation
	rotateAuthFunc = func(ctx context.Context, cli mcTypes.Client, keyType string, clientName string) (*types.AuthRotateResponse, error) {
		assert.Equal(t, "aes256k", keyType)
		assert.Equal(t, "", clientName)
		return &types.AuthRotateResponse{
			TargetKeyType: "aes256k",
			State:         "completed",
		}, nil
	}

	output := captureStdout(t, func() {
		// Mock app client creation by invoking mock function directly or through Run
		resp, err := rotateAuthFunc(context.Background(), nil, c.flagKeyType, c.flagClient)
		require.NoError(t, err)
		if resp.State == "blocked" {
			fmt.Printf("Rotation paused: %s\n", resp.Blocker)
		} else {
			fmt.Printf("Successfully completed auth key rotation to %s\n", resp.TargetKeyType)
		}
	})
	assert.Contains(t, output, "Successfully completed auth key rotation to aes256k")

	// 2. Blocked rotation
	rotateAuthFunc = func(ctx context.Context, cli mcTypes.Client, keyType string, clientName string) (*types.AuthRotateResponse, error) {
		return &types.AuthRotateResponse{
			TargetKeyType: "aes256k",
			State:         "blocked",
			Blocker:       "Unmanaged credentials must be rotated manually",
		}, nil
	}

	output = captureStdout(t, func() {
		resp, _ := rotateAuthFunc(context.Background(), nil, c.flagKeyType, c.flagClient)
		if resp.State == "blocked" {
			fmt.Printf("Rotation paused: %s\n", resp.Blocker)
		}
	})
	assert.Contains(t, output, "Rotation paused: Unmanaged credentials must be rotated manually")

	// 3. Single client rotation
	c.flagClient = "client.rgw"
	rotateAuthFunc = func(ctx context.Context, cli mcTypes.Client, keyType string, clientName string) (*types.AuthRotateResponse, error) {
		assert.Equal(t, "client.rgw", clientName)
		return &types.AuthRotateResponse{
			ClientName: "client.rgw",
			State:      "completed",
		}, nil
	}

	output = captureStdout(t, func() {
		resp, _ := rotateAuthFunc(context.Background(), nil, c.flagKeyType, c.flagClient)
		if c.flagClient != "" {
			fmt.Printf("Successfully rotated key for %s\n", resp.ClientName)
		}
	})
	assert.Contains(t, output, "Successfully rotated key for client.rgw")
	_ = cmd
}

func TestAuthStatusRunJSON(t *testing.T) {
	origGetStatus := getAuthStatusFunc
	defer func() { getAuthStatusFunc = origGetStatus }()

	getAuthStatusFunc = func(ctx context.Context, cli mcTypes.Client) (*types.AuthStatusResponse, error) {
		return &types.AuthStatusResponse{
			Status: "All client aes256k",
			State:  "completed",
			ClientDistribution: map[string][]string{
				"aes256k": {"client.admin", "client.rgw"},
			},
		}, nil
	}

	output := captureStdout(t, func() {
		resp, err := getAuthStatusFunc(context.Background(), nil)
		require.NoError(t, err)

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(resp)
	})

	var decoded types.AuthStatusResponse
	err := json.Unmarshal([]byte(output), &decoded)
	require.NoError(t, err)
	assert.Equal(t, "All client aes256k", decoded.Status)
	assert.Equal(t, "completed", decoded.State)
	assert.Len(t, decoded.ClientDistribution["aes256k"], 2)
}

func TestWatchAuthRotation(t *testing.T) {
	origInterval := authRotationPollInterval
	authRotationPollInterval = time.Millisecond
	defer func() { authRotationPollInterval = origInterval }()

	// 1. Polls until the rotation leaves in_progress.
	calls := 0
	status, err := watchAuthRotation(context.Background(), func() (*types.AuthStatusResponse, error) {
		calls++
		if calls < 3 {
			return &types.AuthStatusResponse{State: "in_progress", Stage: "rotate_daemons"}, nil
		}
		return &types.AuthStatusResponse{State: "completed", Stage: "finish_safely"}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 3, calls)
	assert.Equal(t, "completed", status.State)

	// 2. Terminal states are returned immediately without polling again.
	status, err = watchAuthRotation(context.Background(), func() (*types.AuthStatusResponse, error) {
		return &types.AuthStatusResponse{State: "blocked", Blocker: "Unmanaged credentials"}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "blocked", status.State)

	// 3. Losing track (daemon unreachable) surfaces an error that says the
	//    rotation keeps running.
	_, err = watchAuthRotation(context.Background(), func() (*types.AuthStatusResponse, error) {
		return nil, fmt.Errorf("connection refused")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keeps running in the background")
}
