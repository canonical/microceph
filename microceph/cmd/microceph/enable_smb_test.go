package main

import (
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
		flagClusterID:      "files",
		flagTarget:         "node-a",
		flagDefineUserPass: []string{"smbuser%secret"},
		flagUserGroupRefs:  []string{"existing-users"},
		flagBindNetworks:   []string{"192.0.2.0/24"},
		flagPort:           1445,
		wait:               true,
		common:             &CmdControl{FlagStateDir: "/state"},
	}

	err := command.Run(nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "node-a", target)
	assert.Equal(t, types.ManagedSMBService{
		ClusterID:      "files",
		DefineUserPass: []string{"smbuser%secret"},
		UserGroupRefs:  []string{"existing-users"},
		BindNetworks:   []string{"192.0.2.0/24"},
		Port:           1445,
		Wait:           true,
	}, received)
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
