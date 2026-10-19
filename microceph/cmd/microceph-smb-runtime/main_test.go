package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeRejectsManagementAndArbitraryExecution(t *testing.T) {
	for _, role := range []string{"compile", "publish", "publish-config", "prepare-ctdb", "ready", "launch", "nodes", "control", "/bin/sh"} {
		err := run(context.Background(), []string{role, t.TempDir()})
		require.EqualError(t, err, "unsupported SMB runtime role")
	}
	require.Error(t, run(context.Background(), nil))
	require.EqualError(t, run(context.Background(), []string{"smbd", t.TempDir(), "--command=/bin/sh"}), "unexpected SMB runtime arguments")
	require.EqualError(t, run(context.Background(), []string{"ctdb-control", t.TempDir()}), "unexpected SMB runtime arguments")
}
