package ceph

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

func TestSMBRetirementPreservesFailedOwnerUntilVerifiedStop(t *testing.T) {
	for _, scenario := range []string{"missing-local-file", "stop-failure", "retirement-failure", "owner-unlink-failure", "replacement-identity"} {
		t.Run(scenario, func(t *testing.T) {
			runSMBStartup(t, "compile", true, "", func(state interfaces.StateInterface, r *smbStartupRunner) {
				r.failure = ""
				ownerPath := filepath.Join(smbBase(), "owner.json")
				before, err := os.ReadFile(ownerPath)
				require.NoError(t, err)
				var retained string
				require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&retained))
				switch scenario {
				case "missing-local-file":
					require.NoError(t, os.Remove(ownerPath))
				case "stop-failure":
					r.failure = "stop"
				case "retirement-failure":
					_, err = r.db.Exec(`CREATE TRIGGER fail_retire BEFORE UPDATE ON service_groups BEGIN SELECT RAISE(ABORT,'retire failed'); END;`)
					require.NoError(t, err)
				case "owner-unlink-failure":
					r.onStop = func() {
						require.NoError(t, os.Remove(ownerPath))
						require.NoError(t, os.Mkdir(ownerPath, 0700))
						require.NoError(t, os.WriteFile(filepath.Join(ownerPath, "block"), []byte("block"), 0600))
					}
				case "replacement-identity":
					_, err = r.db.Exec(`DELETE FROM core_cluster_members; INSERT INTO core_cluster_members VALUES(2,'a','192.0.2.1:7443')`)
					require.NoError(t, err)
				}
				r.events = nil
				err = DisableSMB(context.Background(), state, "alpha", false)
				if scenario == "missing-local-file" {
					require.NoError(t, err, "SQL reservation is authoritative even before owner-file publication")
					var raw string
					require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
					require.Contains(t, raw, `"owners":{}`)
					require.Contains(t, raw, `"ranks":{"1":0}`)
				} else {
					require.Error(t, err)
					var raw string
					require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
					if scenario == "owner-unlink-failure" {
						require.Contains(t, raw, `"owners":{}`, "retire before discarding local evidence")
						require.NoError(t, os.RemoveAll(ownerPath))
						require.NoError(t, os.WriteFile(ownerPath, before, 0600))
						r.onStop = nil
						require.NoError(t, DisableSMB(context.Background(), state, "alpha", false), "cleanup retry must not lose exact retired identity")
					} else {
						require.Equal(t, retained, raw)
						owner, e := os.ReadFile(ownerPath)
						require.NoError(t, e)
						require.Equal(t, before, owner)
					}
				}
			})
		})
	}
}

func TestSMBMemberRemovalIncludesFailedOwnerAndForceRetainsUncertainty(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "forced"}[force], func(t *testing.T) {
			runSMBStartup(t, "compile", true, "", func(state interfaces.StateInterface, r *smbStartupRunner) {
				r.failure = "stop"
				require.Equal(t, force, retireSMBForRemoval(context.Background(), state, force) == nil)
				var raw string
				require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
				require.Contains(t, raw, `"owners":{"1":"a"}`)
				_, err := os.Stat(filepath.Join(smbBase(), "owner.json"))
				require.NoError(t, err)
				r.failure = ""
				require.NoError(t, retireSMBForRemoval(context.Background(), state, false))
				require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
				require.Contains(t, raw, `"owners":{}`)
			})
		})
	}
}

func TestSMBDaemonRestartObservesConfiguredOwnerWithoutPlacement(t *testing.T) {
	python := os.Getenv("MICROCEPH_SMB_TEST_PYTHON")
	if python == "" {
		t.Skip("requires pinned real compiler bridge")
	}
	runSMBStartup(t, "", true, python, func(state interfaces.StateInterface, r *smbStartupRunner) {
		old := getServicesForHost
		defer func() { getServicesForHost = old }()
		getServicesForHost = func(context.Context, interfaces.StateInterface, string) ([]database.Service, error) {
			return []database.Service{{Service: "mon", Member: "a"}}, nil
		}
		oldGroups := database.GroupedServicesQuery
		defer func() { database.GroupedServicesQuery = oldGroups }()
		gs := mocks.NewGroupedServiceQueryIntf(t)
		gs.On("GetGroupedServicesOnHost", mock.Anything, mock.Anything).Return([]database.GroupedService{{Service: "smb", GroupID: "alpha", Member: "a"}}, nil).Once()
		database.GroupedServicesQuery = gs
		r.refresh = true
		r.events = nil
		reEnableServices(context.Background(), state)
		require.NotContains(t, r.events, "compile")
		require.NotContains(t, r.events, "restart smbd")
	})
}

func TestSMBExplicitRejoinAfterRetirementKeepsHistory(t *testing.T) {
	python := os.Getenv("MICROCEPH_SMB_TEST_PYTHON")
	if python == "" {
		t.Skip("requires pinned real compiler bridge")
	}
	runSMBStartup(t, "", true, python, func(state interfaces.StateInterface, r *smbStartupRunner) {
		require.NoError(t, DisableSMB(context.Background(), state, "alpha", false))
		// A new core SQL identity on the same name/address is a new PNN, not an
		// undelete of the old identity. Exact-identity re-enable is covered separately.
		_, err := r.db.Exec(`DELETE FROM core_cluster_members; INSERT INTO core_cluster_members VALUES(2,'a','192.0.2.1:7443')`)
		require.NoError(t, err)
		r.expectedIdentity = "2"
		r.expectedPNN = 1
		require.NoError(t, enableSMBLocked(context.Background(), state, "alpha", true))
		var raw string
		require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
		require.Contains(t, raw, `"ranks":{"1":0,"2":1}`)
		require.Contains(t, raw, `"owners":{"2":"a"}`)
		nodes, err := os.ReadFile(filepath.Join(r.root, "ctdb/nodes"))
		require.NoError(t, err)
		require.Equal(t, "#\n192.0.2.1\n", string(nodes))
	})
}
