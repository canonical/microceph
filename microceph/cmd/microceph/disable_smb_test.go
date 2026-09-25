package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCmdDisableSMBSendsManagedRequest(t *testing.T) {
	originalDisable := disableManagedSMBServiceFunc
	t.Cleanup(func() {
		disableManagedSMBServiceFunc = originalDisable
	})
	var clusterID string
	var target string
	disableManagedSMBServiceFunc = func(_ string, value string, member string) error {
		clusterID = value
		target = member
		return nil
	}
	command := &cmdDisableSMB{
		flagClusterID: "files",
		flagTarget:    "node-b",
		common:        &CmdControl{FlagStateDir: "/state"},
	}

	err := command.Run(nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "files", clusterID)
	assert.Equal(t, "node-b", target)
}

func TestCmdDisableSMBRejectsInvalidClusterID(t *testing.T) {
	command := &cmdDisableSMB{}

	err := command.Run(nil, nil)

	assert.ErrorContains(t, err, "valid cluster ID")
}
