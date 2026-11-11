package ceph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
)

type smbServiceCommandRecorder struct {
	calls  [][]string
	err    error
	status string
}

// RunCommand records the legacy runner interface without executing host tools.
func (r *smbServiceCommandRecorder) RunCommand(name string, args ...string) (string, error) {
	return r.RunCommandContext(context.Background(), name, args...)
}

// RunCommandContext records snapctl requests and provides controlled observations.
func (r *smbServiceCommandRecorder) RunCommandContext(_ context.Context, name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)
	if r.err != nil {
		return "", r.err
	}
	if len(args) == 2 && args[0] == "services" {
		if r.status != "" {
			return r.status, nil
		}
		return args[1] + " disabled inactive -\n", nil
	}
	return "", nil
}

func useSMBServiceCommands(t *testing.T) *smbServiceCommandRecorder {
	t.Helper()
	old := common.ProcessExec
	recorder := &smbServiceCommandRecorder{}
	common.ProcessExec = recorder
	t.Cleanup(func() { common.ProcessExec = old })
	return recorder
}

func TestSMBServiceDispatchRejectsUnknownRolesBeforeCommands(t *testing.T) {
	recorder := useSMBServiceCommands(t)
	ops := smbSnapOps(t.TempDir())
	for _, role := range []string{"", "/bin/sh", "ctdb-control", "unknown", "ctdbd", "winbindd", "cephfs-proxy", "smbmetrics"} {
		t.Run(role, func(t *testing.T) {
			require.ErrorContains(t, ops.start(context.Background(), role), "unsupported SMB service")
			require.ErrorContains(t, ops.stop(role), "unsupported SMB service")
		})
	}
	require.Empty(t, recorder.calls)
}

func TestSMBServiceDispatchActuallyStartsAndStopsServices(t *testing.T) {
	recorder := useSMBServiceCommands(t)
	ops := smbSnapOps(t.TempDir())
	for _, role := range []string{"smbd"} {
		recorder.calls = nil
		require.NoError(t, ops.start(context.Background(), role))
		require.Equal(t, [][]string{{"snapctl", "start", "microceph." + role, "--enable"}}, recorder.calls)
		recorder.calls = nil
		require.NoError(t, ops.stop(role))
		require.Equal(t, [][]string{
			{"snapctl", "stop", "microceph." + role, "--disable"},
			{"snapctl", "services", "microceph." + role},
		}, recorder.calls)
	}
}

func TestSMBServiceDispatchPreservesCommandFailures(t *testing.T) {
	recorder := useSMBServiceCommands(t)
	failure := errors.New("snapd service operation failed")
	recorder.err = failure
	ops := smbSnapOps(t.TempDir())
	require.ErrorIs(t, ops.start(context.Background(), "smbd"), failure)
	require.ErrorIs(t, ops.stop("smbd"), failure)
	require.Len(t, recorder.calls, 2, "a failed stop must not be reported as confirmed inactive")
}

func TestSMBServiceTableContainsOnlyIndependentUpstreamServices(t *testing.T) {
	recorder := useSMBServiceCommands(t)
	table := newSMBServiceTable(t.TempDir())
	require.Len(t, table, 1)
	for _, role := range []string{"smbd"} {
		require.Contains(t, table, role)
	}
	delete(table, "smbd")
	require.Contains(t, newSMBServiceTable(t.TempDir()), "smbd", "instances must not share a mutable global registry")
	require.Empty(t, recorder.calls, "constructing the component must not start or stop services")
}

func TestSMBServiceMethodsRespectCancelledOperations(t *testing.T) {
	recorder := useSMBServiceCommands(t)
	service := newSMBServiceTable(t.TempDir())["smbd"]
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, service.Start(ctx), context.Canceled)
	require.ErrorIs(t, service.Stop(ctx), context.Canceled)
	require.ErrorIs(t, service.Ready(ctx), context.Canceled)
	require.Empty(t, recorder.calls)
}

func TestSMBServiceReadyDelegatesWithoutServiceControl(t *testing.T) {
	recorder := useSMBServiceCommands(t)
	root := t.TempDir()
	old := smbReadyNativeFunc
	t.Cleanup(func() { smbReadyNativeFunc = old })
	calls := 0
	smbReadyNativeFunc = func(ctx context.Context, path, role string) error {
		require.Equal(t, root, path)
		require.Equal(t, "smbd", role)
		_, bounded := ctx.Deadline()
		require.True(t, bounded)
		calls++
		return nil
	}
	require.NoError(t, newSMBServiceTable(root)["smbd"].Ready(context.Background()))
	require.Equal(t, 1, calls)
	require.Empty(t, recorder.calls, "functional observation must not start, restart or stop a service")
}

func TestSMBServiceReadyUsesStandardBudgets(t *testing.T) {
	old := smbReadyNativeFunc
	t.Cleanup(func() { smbReadyNativeFunc = old })
	for _, test := range []struct {
		role   string
		budget time.Duration
	}{
		{"smbd", time.Minute},
	} {
		t.Run(test.role, func(t *testing.T) {
			smbReadyNativeFunc = func(ctx context.Context, _, _ string) error {
				deadline, bounded := ctx.Deadline()
				require.True(t, bounded)
				remaining := time.Until(deadline)
				require.Greater(t, remaining, test.budget-time.Second)
				require.LessOrEqual(t, remaining, test.budget)
				return nil
			}
			require.NoError(t, newSMBServiceTable(t.TempDir())[test.role].Ready(context.Background()))
		})
	}
}

func TestSMBServiceReadyDoesNotReportSuccessAfterCancellation(t *testing.T) {
	useSMBServiceCommands(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := smbReadyNativeFunc
	t.Cleanup(func() { smbReadyNativeFunc = old })
	smbReadyNativeFunc = func(context.Context, string, string) error {
		cancel()
		return nil
	}
	require.ErrorIs(t, newSMBServiceTable(t.TempDir())["smbd"].Ready(ctx), context.Canceled)
}

type smbCancellationCommandRunner struct {
	entered    chan context.Context
	release    chan struct{}
	starts     []string
	stopErrors []error
}

// RunCommand implements the legacy process interface without executing commands.
func (r *smbCancellationCommandRunner) RunCommand(name string, args ...string) (string, error) {
	return r.RunCommandContext(context.Background(), name, args...)
}

// RunCommandContext blocks activation until cancellation or explicit test cleanup.
func (r *smbCancellationCommandRunner) RunCommandContext(ctx context.Context, name string, args ...string) (string, error) {
	if len(args) > 0 && args[0] == "start" {
		r.starts = append(r.starts, args[1])
		r.entered <- ctx
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-r.release:
			return "", nil
		}
	}
	if len(args) > 0 && args[0] == "stop" {
		r.stopErrors = append(r.stopErrors, ctx.Err())
	}
	if len(args) == 2 && args[0] == "services" {
		return args[1] + " disabled inactive -\n", nil
	}
	return "", nil
}

// TestSMBActivationCancellationReachesCommandAndCompensates checks transient command and cleanup lifetimes.
func TestSMBActivationCancellationReachesCommandAndCompensates(t *testing.T) {
	runner := &smbCancellationCommandRunner{entered: make(chan context.Context, 1), release: make(chan struct{})}
	old := common.ProcessExec
	common.ProcessExec = runner
	t.Cleanup(func() { common.ProcessExec = old })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	ops := smbSnapOps(root)
	ready := false
	ops.ready = func(context.Context, string) error { ready = true; return nil }
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- startSMBApps(ctx, root, []string{"smbd"}, ops)
	}()
	defer func() {
		cancel()
		close(runner.release)
		<-finished
	}()
	var commandCtx context.Context
	select {
	case commandCtx = <-runner.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("activation did not reach the service command")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("activation did not cancel its in-flight command promptly")
	}
	require.ErrorIs(t, commandCtx.Err(), context.Canceled)
	require.Equal(t, []string{"microceph.smbd"}, runner.starts, "cancellation must not start a later role")
	require.False(t, ready, "cancelled activation must not proceed to readiness")
	require.Len(t, runner.stopErrors, len(smbStopOrder))
	for _, stopErr := range runner.stopErrors {
		require.NoError(t, stopErr, "compensation must not inherit placement cancellation")
	}
	require.NoError(t, checkSMBStartIntent(root), "confirmed compensation must permit a later explicit retry")
}

func TestSMBServiceStopRequiresObservedInactiveState(t *testing.T) {
	recorder := useSMBServiceCommands(t)
	recorder.status = "microceph.smbd disabled active -\n"
	require.ErrorContains(t, newSMBServiceTable(t.TempDir())["smbd"].Stop(context.Background()), "exit is unconfirmed")
	require.Len(t, recorder.calls, 2)
}
