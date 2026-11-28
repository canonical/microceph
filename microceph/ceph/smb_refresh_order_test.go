package ceph

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSMBRefreshOptionAliasOrderIsAConfigurationChange(t *testing.T) {
	state, runner := refreshFixture(t)
	runner.config = strings.Replace(runner.config, `"globals":{}`, `"globals":{"labels":{"options":{"server string":"first","serverstring":"second"}}}`, 1)
	runner.config = strings.Replace(runner.config, `"globals":[]`, `"globals":["labels"]`, 1)
	require.NoError(t, refreshSMBLocked(context.Background(), state))
	runner.commands = nil

	// Samba ignores whitespace in parameter names and the last alias wins.
	// Sorting these keys would hide a real change behind the unchanged hash.
	previous := runner.config
	runner.config = strings.Replace(runner.config, `"server string":"first","serverstring":"second"`, `"serverstring":"second","server string":"first"`, 1)
	require.NotEqual(t, previous, runner.config)
	require.NoError(t, refreshSMBLocked(context.Background(), state))
	require.Contains(t, strings.Join(runner.commands, "\n"), "snapctl restart microceph.smbd")
}
