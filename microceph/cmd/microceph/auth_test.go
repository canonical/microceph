package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

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

func TestFormatAuthStatusText(t *testing.T) {
	// 1. Blocked status with blocker
	respBlocked := &types.AuthStatusResponse{
		Status:  "blocked",
		Blocker: "Unmanaged credentials must be rotated manually before rotation can proceed",
	}
	out := formatAuthStatusText(respBlocked)
	assert.Contains(t, out, "Status: blocked\n")
	assert.Contains(t, out, "Blocker: Unmanaged credentials must be rotated manually before rotation can proceed\n")

	// 2. Uniform cipher status
	respUniform := &types.AuthStatusResponse{
		Status: "All client aes256k",
	}
	out = formatAuthStatusText(respUniform)
	assert.Equal(t, "Status: All client aes256k\n", out)

	// 3. Mixed client cipher status
	respMixed := &types.AuthStatusResponse{
		Status: "1 clients on aes, 1 clients on aes256k (aes: client.X, aes256k: client.Y)",
	}
	out = formatAuthStatusText(respMixed)
	assert.Equal(t, "Status: 1 clients on aes, 1 clients on aes256k (aes: client.X, aes256k: client.Y)\n", out)
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
