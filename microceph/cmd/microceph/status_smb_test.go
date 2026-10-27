package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
)

func TestSMBStatusCommandSurface(t *testing.T) {
	status := (&cmdStatus{common: &CmdControl{}}).Command()
	cmd, _, err := status.Find([]string{"smb"})
	require.NoError(t, err)
	require.Equal(t, "smb", cmd.Name())
	require.NotNil(t, cmd.Flags().Lookup("target"))
	require.Nil(t, cmd.Flags().Lookup("password"))
	require.Nil(t, cmd.Flags().Lookup("repair"))
	require.NotNil(t, status.RunE, "bare status retains its existing handler")
}

func TestSMBStatusCommandRejectsUnknownSubcommandBeforeTransport(t *testing.T) {
	_, err := executeRoot("status", "bogus")
	require.ErrorContains(t, err, `unknown command "bogus"`)
}

func TestSMBStatusCommandOutputsExactNonsecretJSONAndForwardsTarget(t *testing.T) {
	for _, target := range []string{"", "gateway-a"} {
		t.Run("target-"+target, func(t *testing.T) {
			old := getSMBServiceFunc
			t.Cleanup(func() { getSMBServiceFunc = old })
			type contextKey struct{}
			ctx := context.WithValue(context.Background(), contextKey{}, "request")
			getSMBServiceFunc = func(got context.Context, _ mcTypes.Client, selected string) (types.SMBStatus, error) {
				require.Equal(t, "request", got.Value(contextKey{}))
				require.Equal(t, target, selected)
				return types.SMBStatus{ClusterID: "alpha", StopRequested: true, Error: "stop is unconfirmed"}, nil
			}
			common := &CmdControl{FlagStateDir: t.TempDir()}
			cmd := (&cmdStatus{common: common}).Command()
			var output bytes.Buffer
			cmd.SetOut(&output)
			args := []string{"smb"}
			if target != "" {
				args = append(args, "--target", target)
			}
			cmd.SetArgs(args)
			require.NoError(t, cmd.ExecuteContext(ctx))
			require.Equal(t, "{\"cluster_id\":\"alpha\",\"ready\":false,\"stop_requested\":true,\"error\":\"stop is unconfirmed\"}\n", output.String())
		})
	}
}

func TestSMBStatusCommandPreservesTransportFailureWithoutOutput(t *testing.T) {
	old := getSMBServiceFunc
	t.Cleanup(func() { getSMBServiceFunc = old })
	failure := errors.New("status transport failed")
	getSMBServiceFunc = func(context.Context, mcTypes.Client, string) (types.SMBStatus, error) {
		return types.SMBStatus{}, failure
	}
	cmd := (&cmdStatus{common: &CmdControl{FlagStateDir: t.TempDir()}}).Command()
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"smb"})
	require.ErrorIs(t, cmd.ExecuteContext(context.Background()), failure)
	require.Empty(t, output.String())
}
