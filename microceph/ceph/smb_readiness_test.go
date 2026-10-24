package ceph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
)

// Reject snap run, daemon self-execution and former helper subprocesses.
// Fixed native client execution is exercised independently by the CTDB fixture.
type smbConfinedReadinessRunner struct{ t *testing.T }

func (r smbConfinedReadinessRunner) RunCommand(name string, args ...string) (string, error) {
	return r.RunCommandContext(context.Background(), name, args...)
}
func (r smbConfinedReadinessRunner) RunCommandContext(_ context.Context, name string, args ...string) (string, error) {
	r.t.Errorf("unexpected readiness subprocess %s %q", name, args)
	return "", errors.New("subprocess forbidden")
}

func TestSMBReadinessUsesTypedNativeControlsWithoutHelperIPC(t *testing.T) {
	root, snap := nativeCTDBFixture(t)
	old := common.ProcessExec
	common.ProcessExec = smbConfinedReadinessRunner{t: t}
	defer func() { common.ProcessExec = old }()
	require.NoError(t, smbReadyNative(context.Background(), root, "ctdbd"))
	trace, err := os.ReadFile(filepath.Join(snap, "commands"))
	require.NoError(t, err)
	require.Contains(t, string(trace), "pnn\n")
	require.Contains(t, string(trace), "nodestatus 2\n")
	require.Contains(t, string(trace), "status\n")
	require.NoError(t, smbAtomicWrite(filepath.Join(filepath.Dir(root), "owner.json"), []byte("other")))
	require.Error(t, smbReadyNative(context.Background(), root, "ctdbd"))
	require.Error(t, smbSnapOps(root).ready(context.Background(), "ctdb-nodes"))
}

func TestSMBClusteredReadinessGatesNativeConsumersWithoutMonitorService(t *testing.T) {
	for _, failure := range []string{"success", "ctdbd-start", "ctdbd-ready"} {
		t.Run(failure, func(t *testing.T) {
			events := []string{}
			started := map[string]bool{}
			ops := smbAppOps{
				prepare: func(ctx context.Context, role string) error {
					events = append(events, "prepare "+role)
					require.False(t, started[role])
					return nil
				},
				start: func(role string) error {
					events = append(events, "start "+role)
					started[role] = true
					if failure == role+"-start" {
						return errors.New("uncertain start")
					}
					return nil
				},
				ready: func(ctx context.Context, role string) error {
					events = append(events, "ready "+role)
					require.True(t, started["ctdbd"])
					if failure == role+"-ready" {
						return errors.New("unhealthy CTDB")
					}
					return nil
				},
				stop: func(role string) error { events = append(events, "stop "+role); return nil },
			}
			err := startSMBApps(context.Background(), t.TempDir(), []string{"ctdbd", "smbd"}, ops)
			if failure == "success" {
				require.NoError(t, err)
				require.Equal(t, []string{"prepare ctdbd", "start ctdbd", "ready ctdbd", "prepare smbd", "start smbd", "ready smbd"}, events)
			} else {
				require.Error(t, err)
				require.NotContains(t, events, "prepare smbd", "unconfirmed CTDB gates SAM preparation too")
				require.NotContains(t, events, "start smbd")
				for _, role := range smbStopOrder {
					require.Contains(t, events, "stop "+role)
				}
			}
		})
	}
}
