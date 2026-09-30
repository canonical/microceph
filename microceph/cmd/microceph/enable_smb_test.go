package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
)

func TestCmdEnableSMBSendsManagedRequest(t *testing.T) {
	originalEnable := enableManagedSMBServiceFunc
	t.Cleanup(func() {
		enableManagedSMBServiceFunc = originalEnable
	})
	var received types.ManagedSMBService
	var target string
	enableManagedSMBServiceFunc = func(_ string, request *types.ManagedSMBService, value string) error {
		received = *request
		target = value
		return nil
	}
	command := &cmdEnableSMB{
		flagClusterID:     "files",
		flagTarget:        "node-a",
		flagUserGroupRefs: []string{"existing-users"},
		flagBindNetworks:  []string{"192.0.2.0/24"},
		flagPort:          1445,
		wait:              true,
		common:            &CmdControl{FlagStateDir: "/state"},
	}

	err := command.Run(nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "node-a", target)
	assert.Equal(t, types.ManagedSMBService{
		ClusterID:     "files",
		UserGroupRefs: []string{"existing-users"},
		BindNetworks:  []string{"192.0.2.0/24"},
		Port:          1445,
		Wait:          true,
	}, received)
}

func TestCmdEnableSMBCredentialsFromFileAndStdin(t *testing.T) {
	for _, fromStdin := range []bool{false, true} {
		t.Run(map[bool]string{true: "stdin", false: "file"}[fromStdin], func(t *testing.T) {
			original := enableManagedSMBServiceFunc
			t.Cleanup(func() { enableManagedSMBServiceFunc = original })
			var received *types.ManagedSMBService
			enableManagedSMBServiceFunc = func(_ string, request *types.ManagedSMBService, _ string) error { received = request; return nil }
			body := `{"users":[{"name":"alice","password":"a-secret"},{"name":"bob","password":"b-secret"}]}`
			path := "-"
			if !fromStdin {
				path = filepath.Join(t.TempDir(), "users.json")
				require.NoError(t, os.WriteFile(path, []byte(body), 0600))
			}
			c := &cmdEnableSMB{common: &CmdControl{FlagStateDir: "/state"}}
			cmd := c.Command()
			require.Nil(t, cmd.Flags().Lookup("define-user-pass"))
			cmd.SetIn(strings.NewReader(body))
			cmd.SetArgs([]string{"--cluster-id", "files", "--credentials-file", path})
			require.NoError(t, cmd.Execute())
			require.NotNil(t, received.Credentials)
			require.Equal(t, "alice", received.Credentials.Users[0].Name)
			require.Equal(t, "b-secret", received.Credentials.Users[1].Password)
		})
	}
}

func TestCmdEnableSMBCredentialsErrorsDoNotEchoInput(t *testing.T) {
	for _, body := range []string{
		`{"users":[{"name":"alice","password":"secret","secret-as-field":true}]}`,
		`{"users":"secret-as-value"}`, `{"users":[]} {"secret":true}`,
	} {
		c := &cmdEnableSMB{common: &CmdControl{}}
		cmd := c.Command()
		cmd.SetIn(strings.NewReader(body))
		cmd.SetArgs([]string{"--cluster-id", "files", "--credentials-file", "-"})
		err := cmd.Execute()
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestCmdEnableSMBRejectsCredentialAndReferenceCombination(t *testing.T) {
	c := &cmdEnableSMB{common: &CmdControl{}}
	cmd := c.Command()
	cmd.SetArgs([]string{"--cluster-id", "files", "--credentials-file", "-", "--user-group-ref", "users"})
	require.ErrorContains(t, cmd.Execute(), "either")
}

func TestCmdEnableSMBPreservesClusteringOmission(t *testing.T) {
	for _, mode := range []string{"", "always", "never"} {
		t.Run(mode, func(t *testing.T) {
			original := enableManagedSMBServiceFunc
			t.Cleanup(func() { enableManagedSMBServiceFunc = original })
			enableManagedSMBServiceFunc = func(_ string, request *types.ManagedSMBService, _ string) error {
				if mode == "" {
					require.Nil(t, request.Clustering)
				} else {
					require.NotNil(t, request.Clustering)
					require.Equal(t, mode, *request.Clustering)
				}
				return nil
			}
			c := &cmdEnableSMB{common: &CmdControl{}}
			cmd := c.Command()
			args := []string{"--cluster-id", "files"}
			if mode != "" {
				args = append(args, "--clustering", mode)
			}
			cmd.SetArgs(args)
			require.NoError(t, cmd.Execute())
		})
	}
}

func TestCmdEnableSMBRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name     string
		command  cmdEnableSMB
		expected string
	}{
		{
			name:     "cluster ID",
			command:  cmdEnableSMB{},
			expected: "valid cluster ID",
		},
		{
			name: "two bind selectors",
			command: cmdEnableSMB{
				flagClusterID:     "files",
				flagBindAddresses: []string{"192.0.2.10"},
				flagBindNetworks:  []string{"192.0.2.0/24"},
			},
			expected: "either `--bind-address` or `--bind-network`",
		},
		{
			name: "bind address",
			command: cmdEnableSMB{
				flagClusterID:     "files",
				flagBindAddresses: []string{"invalid"},
			},
			expected: "could not parse the given `--bind-address`",
		},
		{
			name: "bind network",
			command: cmdEnableSMB{
				flagClusterID:    "files",
				flagBindNetworks: []string{"invalid"},
			},
			expected: "could not parse the given `--bind-network`",
		},
		{
			name: "port",
			command: cmdEnableSMB{
				flagClusterID: "files",
				flagPort:      65536,
			},
			expected: "valid port number",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.command.Run(nil, nil)
			assert.ErrorContains(t, err, test.expected)
		})
	}
}
