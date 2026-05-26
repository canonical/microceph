package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCmdDiskEncryptionSupportUnsupported(t *testing.T) {
	orig := getEncryptionSupportFunc
	defer func() { getEncryptionSupportFunc = orig }()
	getEncryptionSupportFunc = func(_ context.Context, _ string) (bool, string, error) {
		return false, "missing dm_crypt module", nil
	}

	cmd := &cmdDiskEncryptionSupport{common: &CmdControl{}}
	err := cmd.Run(nil, []string{})
	require.Error(t, err)
	assert.ErrorContains(t, err, "missing dm_crypt module")
}

func TestCmdDiskEncryptionSupportSupported(t *testing.T) {
	orig := getEncryptionSupportFunc
	defer func() { getEncryptionSupportFunc = orig }()
	getEncryptionSupportFunc = func(_ context.Context, _ string) (bool, string, error) {
		return true, "", nil
	}

	cmd := &cmdDiskEncryptionSupport{common: &CmdControl{}}
	var err error
	out := captureStdout(t, func() {
		err = cmd.Run(nil, []string{})
	})
	assert.NoError(t, err)
	assert.Contains(t, out, "Encryption supported.")
}

func TestCmdDiskEncryptionSupportTransportError(t *testing.T) {
	orig := getEncryptionSupportFunc
	defer func() { getEncryptionSupportFunc = orig }()
	getEncryptionSupportFunc = func(_ context.Context, _ string) (bool, string, error) {
		return false, "", fmt.Errorf("connection refused")
	}

	cmd := &cmdDiskEncryptionSupport{common: &CmdControl{}}
	err := cmd.Run(nil, []string{})
	require.Error(t, err)
	assert.ErrorContains(t, err, "connection refused")
}
