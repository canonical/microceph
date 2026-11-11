package ceph

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSMBIndirectionRejectedBeforeAdmissionAndProjectionEffects(t *testing.T) {
	for _, scope := range []string{"global", "share", "default"} {
		for _, key := range []string{"include", "IN CL\tUDE", "copy", "CONFIG FILE", "config backend", "registry shares"} {
			t.Run(scope+"/"+key, func(t *testing.T) {
				state, runner := smbLocalStartupFixture(t, "")
				options := map[string]any{key: "/unexpanded/policy.conf"}
				if key == "config backend" {
					options[key] = "registry"
				}
				if key == "registry shares" {
					options[key] = "yes"
				}
				instance := map[string]any{"globals": []string{}, "shares": []string{}}
				document := map[string]any{"samba-container-config": "v0", "configs": map[string]any{"alpha": instance}, "globals": map[string]any{}, "shares": map[string]any{}}
				switch scope {
				case "global", "default":
					// Global defaults also apply to subsequently declared shares.
					instance["globals"] = []string{"policy"}
					document["globals"] = map[string]any{"policy": map[string]any{"options": options}}
					if scope == "default" {
						instance["shares"] = []string{"data"}
						document["shares"] = map[string]any{"data": map[string]any{"options": map[string]any{"path": "/data"}}}
					}
				case "share":
					instance["shares"] = []string{"data"}
					options["path"] = "/data"
					document["shares"] = map[string]any{"data": map[string]any{"options": options}}
				}
				raw, err := json.Marshal(document)
				require.NoError(t, err)
				runner.config = string(raw)
				t.Run("admission", func(t *testing.T) {
					err = enableSMBLocked(context.Background(), state, "alpha")
					require.ErrorContains(t, err, "indirection")
					var count int
					require.NoError(t, runner.db.QueryRow("SELECT COUNT(*) FROM service_groups").Scan(&count))
					require.Zero(t, count)
					for _, command := range runner.commands {
						require.NotContains(t, command, "client.smb.fs.cluster.alpha")
						require.NotContains(t, command, "snapctl")
					}
					_, err = os.Stat(filepath.Join(smbBase(), "owner.json"))
					require.ErrorIs(t, err, os.ErrNotExist)
					_, err = os.Stat(filepath.Join(smbRoot("alpha"), "config/smb.conf"))
					require.ErrorIs(t, err, os.ErrNotExist)
				})
				t.Run("projection", func(t *testing.T) {
					candidate := smbCandidate{Spec: smbSpec{ClusterID: "alpha"}, Config: raw}
					calls := []string{}
					real := nativeTestBinding(t)
					plan, err := projectSMBNative(candidate, "/native/alpha", func(command string, request any, result any) error {
						calls = append(calls, command)
						return real(command, request, result)
					})
					require.ErrorContains(t, err, "indirection")
					require.Empty(t, plan.Files)
					require.Equal(t, []string{"inspect"}, calls)
				})
			})
		}
	}
}
