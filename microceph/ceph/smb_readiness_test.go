package ceph

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
)

func TestSMBReadinessNeedsDaemonResponseNotOnlyActiveShell(t *testing.T) {
	t.Setenv("SNAP", "/snap/microceph/current")
	runner := mocks.NewRunner(t)
	original := common.ProcessExec
	t.Cleanup(func() { common.ProcessExec = original })
	common.ProcessExec = runner
	runner.On("RunCommandContext", mock.Anything, "snapctl", "services", "microceph.smbd").Return("Service Startup Current Notes\nmicroceph.smbd enabled active -\n", nil).Twice()
	runner.On("RunCommandContext", mock.Anything, "/snap/microceph/current/commands/samba-command", "smbcontrol", "smbd", "ping").Return("", context.DeadlineExceeded).Once()
	runner.On("RunCommandContext", mock.Anything, "/snap/microceph/current/commands/samba-command", "smbcontrol", "smbd", "ping").Return("PONG from pid 123\n", nil).Once()
	require.NoError(t, waitForSMBServiceReady(context.Background(), "smbd", 0))
}

func TestSMBReadinessCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, waitForSMBServiceReady(ctx, "smbd", time.Second), context.Canceled)
}

func TestCTDBLocalReadinessDoesNotRequireEveryPeer(t *testing.T) {
	for _, tc := range []struct {
		status string
		ready  bool
	}{
		{"Number of nodes:2\npnn:0 10.0.0.1 OK (THIS NODE)\npnn:1 10.0.0.2 DISCONNECTED\nRecovery mode:NORMAL (0)\n", true},
		{"pnn:0 10.0.0.1 UNHEALTHY (THIS NODE)\nRecovery mode:NORMAL (0)\n", false},
		{"pnn:0 10.0.0.1 OK (THIS NODE)\nRecovery mode:RECOVERY (1)\n", false},
		{"pnn:0 10.0.0.1 OK\nRecovery mode:NORMAL (0)\n", false},
		{"", false},
	} {
		require.Equal(t, tc.ready, smbCTDBLocallyReady(tc.status), tc.status)
	}
}

func TestSMBReadinessVerifiesAssignedPNN(t *testing.T) {
	t.Setenv("SNAP", "/snap/microceph/current")
	for _, output := range []string{"0\n", "1\n", "invalid"} {
		t.Run(output, func(t *testing.T) {
			runner := mocks.NewRunner(t)
			original := common.ProcessExec
			t.Cleanup(func() { common.ProcessExec = original })
			common.ProcessExec = runner
			runner.On("RunCommandContext", mock.Anything, "/snap/microceph/current/commands/samba-command", "ctdb", "pnn").Return(output, nil).Once()
			err := checkSMBCTDBPNN(context.Background(), 1)
			if output == "1\n" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "PNN")
			}
		})
	}
}

func TestSMBUnchangedPlacementStillChecksReadiness(t *testing.T) {
	original := smbPostPlacementCheckFunc
	t.Cleanup(func() { smbPostPlacementCheckFunc = original })
	calls := 0
	smbPostPlacementCheckFunc = func(_ context.Context, service string) error {
		calls++
		require.Equal(t, "smbd", service)
		return context.DeadlineExceeded
	}
	placement := &SMBServicePlacement{unchanged: true}
	require.ErrorIs(t, placement.PostPlacementCheck(nil), context.DeadlineExceeded)
	require.Equal(t, 1, calls)
}
