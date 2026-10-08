package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
)

func TestCmdDisableSMBSendsTargetedManagedRemovalRequest(t *testing.T) {
	originalDisable := disableManagedSMBServiceFunc
	t.Cleanup(func() {
		disableManagedSMBServiceFunc = originalDisable
	})
	var received *types.ManagedSMBRemoval
	disableManagedSMBServiceFunc = func(_ string, request *types.ManagedSMBRemoval) error {
		received = request
		return nil
	}
	command := &cmdDisableSMB{
		flagClusterID: "files",
		flagTarget:    "node-b",
		common:        &CmdControl{FlagStateDir: "/state"},
	}

	err := command.Run(nil, nil)

	require.NoError(t, err)
	assert.Equal(t, &types.ManagedSMBRemoval{ClusterID: "files", Target: "node-b"}, received)
}

func TestCmdDisableSMBRejectsInvalidClusterID(t *testing.T) {
	command := &cmdDisableSMB{}

	err := command.Run(nil, nil)

	assert.ErrorContains(t, err, "valid cluster ID")
}

func TestCmdDisableSMBPrintsSelectedScopeBeforeMutation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		request types.ManagedSMBRemoval
		output  string
	}{
		{
			name:    "member",
			args:    []string{"--cluster-id", "files", "--target", "node-b"},
			request: types.ManagedSMBRemoval{ClusterID: "files", Target: "node-b"},
			output:  "Removing SMB member",
		},
		{
			name:    "deployment",
			args:    []string{"--cluster-id", "files"},
			request: types.ManagedSMBRemoval{ClusterID: "files"},
			output:  "entire SMB gateway deployment",
		},
		{
			name:    "logical cluster",
			args:    []string{"--cluster-id", "files", "--force"},
			request: types.ManagedSMBRemoval{ClusterID: "files", Force: true},
			output:  "Deleting logical SMB cluster",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalDisable := disableManagedSMBServiceFunc
			t.Cleanup(func() {
				disableManagedSMBServiceFunc = originalDisable
			})
			var received *types.ManagedSMBRemoval
			disableManagedSMBServiceFunc = func(_ string, request *types.ManagedSMBRemoval) error {
				received = request
				return nil
			}
			command := &cmdDisableSMB{common: &CmdControl{FlagStateDir: "/state"}}
			cmd := command.Command()
			output := &bytes.Buffer{}
			cmd.SetOut(output)
			cmd.SetArgs(test.args)

			err := cmd.Execute()

			require.NoError(t, err)
			assert.Equal(t, &test.request, received)
			assert.Contains(t, output.String(), test.output)
			assert.Contains(t, output.String(), "CephFS data")
		})
	}
}

func TestCmdDisableSMBRejectsInvalidSelectorsBeforeMutation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		message string
	}{
		{name: "empty target", args: []string{"--cluster-id", "files", "--target="}, message: "--target must name a server"},
		{name: "invalid target", args: []string{"--cluster-id", "files", "--target", "node/a"}, message: "valid member name"},
		{name: "target and force", args: []string{"--cluster-id", "files", "--target", "node-a", "--force"}, message: "--target cannot be used with --force"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalDisable := disableManagedSMBServiceFunc
			t.Cleanup(func() {
				disableManagedSMBServiceFunc = originalDisable
			})
			disableManagedSMBServiceFunc = func(_ string, _ *types.ManagedSMBRemoval) error {
				t.Fatal("invalid selector must not start a removal")
				return nil
			}
			command := &cmdDisableSMB{common: &CmdControl{FlagStateDir: "/state"}}
			cmd := command.Command()
			cmd.SetArgs(test.args)

			err := cmd.Execute()

			require.ErrorContains(t, err, test.message)
		})
	}
}
