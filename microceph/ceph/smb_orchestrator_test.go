package ceph

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
)

type smbOrchModeRunner struct {
	common.Runner
	backend string
}

func (r smbOrchModeRunner) RunCommandContext(ctx context.Context, name string, args ...string) (string, error) {
	if args[0] == "mgr" {
		return `{"enabled_modules":["smb"]}`, nil
	}
	if args[len(args)-1] == "mgr/smb/update_orchestration" {
		return "true", nil
	}
	if args[len(args)-1] == "mgr/orchestrator/orchestrator" {
		return r.backend, nil
	}
	return r.Runner.RunCommandContext(ctx, name, args...)
}

func TestSMBOrchestratorModeRequiresMicroCephBackend(t *testing.T) {
	old := common.ProcessExec
	t.Cleanup(func() { common.ProcessExec = old })
	for _, backend := range []string{"microceph", "cephadm", ""} {
		t.Run(backend, func(t *testing.T) {
			common.ProcessExec = smbOrchModeRunner{Runner: common.RunnerImpl{}, backend: backend}
			err := ensureSMBModule(context.Background())
			if backend == "microceph" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestSMBOrchestratorRetirementFencesQueuedPlacement(t *testing.T) {
	state, runner := smbLocalStartupFixture(t, "")
	require.NoError(t, RetireOrchestratedSMB(context.Background(), state, "alpha"))
	require.Empty(t, runner.commands, "an absent owner cannot authorize stopping shared apps")
	require.Error(t, checkSMBStartIntent(smbRoot("alpha")))
	payload, err := json.Marshal(map[string]any{"cluster_id": "alpha", "orchestrator_spec": json.RawMessage(runner.spec)})
	require.NoError(t, err)
	var placement SMBServicePlacement
	require.NoError(t, placement.PopulateParams(state, string(payload)))
	require.Error(t, placement.ServiceInit(context.Background(), state))
	require.Empty(t, runner.commands)
}

type smbOrchChangingRunner struct {
	*smbLocalStartupRunner
	changed bool
}

func (r *smbOrchChangingRunner) RunCommandContext(ctx context.Context, name string, args ...string) (string, error) {
	out, err := r.smbLocalStartupRunner.RunCommandContext(ctx, name, args...)
	if name == "rados" && args[len(args)-2] == "spec.smb" && !r.changed {
		r.spec = strings.Replace(r.spec, `"cluster_id":"alpha"`, `"cluster_id":"alpha","custom_ports":{"smb":2445}`, 1)
		r.changed = true
	}
	return out, err
}

func TestSMBOrchestratorRequestGuards(t *testing.T) {
	for _, kind := range []string{"stopped", "changed-policy", "changed-during-read"} {
		t.Run(kind, func(t *testing.T) {
			state, runner := smbLocalStartupFixture(t, "")
			spec := runner.spec
			if kind == "stopped" {
				require.NoError(t, smbAtomicWrite(filepath.Join(smbRoot("alpha"), "stop-requested"), []byte("operator stop\n")))
			} else if kind == "changed-policy" {
				spec = strings.Replace(spec, "config.smb", "different.smb", 1)
			} else {
				common.ProcessExec = &smbOrchChangingRunner{smbLocalStartupRunner: runner}
			}
			payload, err := json.Marshal(map[string]any{"cluster_id": "alpha", "orchestrator_spec": json.RawMessage(spec)})
			require.NoError(t, err)
			var placement SMBServicePlacement
			require.NoError(t, placement.PopulateParams(state, string(payload)))
			require.Error(t, placement.ServiceInit(context.Background(), state))
			_, err = os.Stat(filepath.Join(smbBase(), "owner.json"))
			require.True(t, os.IsNotExist(err), "rejected request must not claim ownership")
			require.False(t, runner.active)
		})
	}
}
