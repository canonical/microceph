package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deleteServiceCall records one request the disable command sent to the daemon.
type deleteServiceCall struct {
	target  string
	service string
}

// stubDeleteService replaces deleteServiceFunc for the test, returning err, and records the requests.
func stubDeleteService(t *testing.T, err error) *[]deleteServiceCall {
	t.Helper()

	calls := []deleteServiceCall{}
	orig := deleteServiceFunc
	t.Cleanup(func() { deleteServiceFunc = orig })
	deleteServiceFunc = func(_ context.Context, _ string, target string, service string) error {
		calls = append(calls, deleteServiceCall{target: target, service: service})
		return err
	}

	return &calls
}

// TestDisableServiceSendsTheService checks that each disable command asks the daemon to disable its
// own service, on this server by default and on the --target server when one is given.
func TestDisableServiceSendsTheService(t *testing.T) {
	for _, service := range []string{"mon", "mgr", "mds", "rbd-mirror"} {
		t.Run(service, func(t *testing.T) {
			calls := stubDeleteService(t, nil)

			_, err := executeRoot("disable", service)
			require.NoError(t, err)

			_, err = executeRoot("disable", service, "--target", "node2")
			require.NoError(t, err)

			assert.Equal(t, []deleteServiceCall{
				{target: "", service: service},
				{target: "node2", service: service},
			}, *calls)
		})
	}
}

// TestDisableServiceReturnsTheDaemonError checks that a refusal from the daemon, such as the MON
// quorum check, fails the command.
func TestDisableServiceReturnsTheDaemonError(t *testing.T) {
	refusal := errors.New("refusing to remove monitor")
	stubDeleteService(t, refusal)

	_, err := executeRoot("disable", "mon")
	require.ErrorIs(t, err, refusal)
}

// TestDisableServiceHelp checks the help text names the service as the enable command does.
func TestDisableServiceHelp(t *testing.T) {
	root := newRootCommand()

	want := map[string]string{
		"mon":        "Disable the MON service on the --target server (default: this server)",
		"mgr":        "Disable the MGR service on the --target server (default: this server)",
		"mds":        "Disable the MDS service on the --target server (default: this server)",
		"rbd-mirror": "Disable the RBD Mirror service on the --target server (default: this server)",
	}
	for service, short := range want {
		cmd, _, err := root.Find([]string{"disable", service})
		require.NoError(t, err)
		require.Equal(t, "microceph disable "+service, cmd.CommandPath())
		assert.Equal(t, short, cmd.Short)
	}
}
