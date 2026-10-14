package ceph

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

func TestSMBRefreshDoesNotProvision(t *testing.T) {
	for _, clustered := range []bool{false, true} {
		for _, scenario := range []string{"healthy-unchanged", "unready-unchanged", "changed", "no-applied", "no-service", "stop", "source-failure", "publish-failure", "restart-failure", "logical-unknown", "logical-deleted", "disabled", "retry-reverted", "torn-source", "unknown-owner"} {
			t.Run(map[bool]string{true: "clustered/", false: "nonclustered/"}[clustered]+scenario, func(t *testing.T) {
				runSMBStartup(t, "", clustered, os.Getenv("MICROCEPH_SMB_TEST_PYTHON"), func(state interfaces.StateInterface, r *smbStartupRunner) {
					root := r.root
					// The fallback process fixture still supplies the actual Go context path.
					if r.python == "" {
						candidate, err := os.ReadFile(filepath.Join(root, "config/candidate.next.json"))
						require.NoError(t, err)
						require.NoError(t, os.WriteFile(filepath.Join(root, "config/candidate.json"), candidate, 0600))
						context := `{"clustered":true,"proxy":false,"smb_port":445}`
						if !clustered {
							context = `{"clustered":false,"proxy":false,"smb_port":445}`
						}
						require.NoError(t, os.WriteFile(filepath.Join(root, "config/context.json"), []byte(context), 0600))
					}
					var ownership string
					require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&ownership))
					applied, err := os.ReadFile(filepath.Join(root, "applied.json"))
					require.NoError(t, err)
					switch scenario {
					case "unready-unchanged":
						r.unready = true
					case "changed":
						r.changed = true
					case "no-applied":
						require.NoError(t, os.Remove(filepath.Join(root, "applied.json")))
					case "unknown-owner":
						_, err = r.db.Exec(`UPDATE service_groups SET config=replace(config,'"owners":{"1":"a"}','"owners":{}')`)
						require.NoError(t, err)
						require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&ownership))
					case "no-service":
						_, err = r.db.Exec("DELETE FROM grouped_services")
						require.NoError(t, err)
					case "stop":
						require.NoError(t, os.WriteFile(filepath.Join(root, "stop-requested"), []byte("stop"), 0600))
					case "source-failure":
						r.sourceFailure = true
					case "publish-failure":
						r.changed = true
						r.failure = "publish"
					case "torn-source":
						r.changed = true
						r.torn = true
					case "logical-unknown":
						r.logical = `{"unexpected":true}`
					case "logical-deleted":
						r.logical = `{"resources":[]}`
						r.containment = true
					case "disabled":
						r.changed = true
						r.disabled = true
					case "retry-reverted":
						r.changed = true
						r.failure = "publish"
						if r.python != "" {
							r.failure = "publish-partial"
						}
					case "restart-failure":
						r.changed = true
						r.failure = "restart"
					}
					// Fail attempted placement even when an idempotent reservation would keep
					// the same values. Final-state assertions alone cannot detect re-entry.
					_, err = r.db.Exec(`CREATE TRIGGER forbid_group_update BEFORE UPDATE ON service_groups BEGIN SELECT RAISE(ABORT,'forbidden owner reservation'); END;
     CREATE TRIGGER forbid_group_insert BEFORE INSERT ON service_groups BEGIN SELECT RAISE(ABORT,'forbidden group creation'); END;
     CREATE TRIGGER forbid_service_insert BEFORE INSERT ON grouped_services BEGIN SELECT RAISE(ABORT,'forbidden enabled service creation'); END;
     CREATE TRIGGER forbid_service_delete BEFORE DELETE ON grouped_services BEGIN SELECT RAISE(ABORT,'forbidden service withdrawal'); END;`)
					require.NoError(t, err)
					if scenario == "logical-deleted" {
						_, err = r.db.Exec("DROP TRIGGER forbid_service_delete")
						require.NoError(t, err)
					}
					r.refresh = true
					r.events = nil
					err = refreshSMBLocked(context.Background(), state)
					if scenario == "unready-unchanged" || scenario == "source-failure" || scenario == "publish-failure" || scenario == "restart-failure" || scenario == "logical-unknown" || scenario == "logical-deleted" || scenario == "disabled" || scenario == "retry-reverted" || scenario == "torn-source" || scenario == "unknown-owner" {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
					}
					if scenario == "changed" {
						require.Contains(t, r.events, "publish")
						require.Contains(t, r.events, "restart smbd")
						next, e := os.ReadFile(filepath.Join(root, "applied.json"))
						require.NoError(t, e)
						require.NotEqual(t, applied, next)
						if r.python != "" {
							config, e := os.ReadFile(filepath.Join(root, "config/smb.conf"))
							require.NoError(t, e)
							require.Contains(t, string(config), "[data]")
							accounts, e := os.ReadFile(filepath.Join(root, "config/candidate.json"))
							require.NoError(t, e)
							require.Contains(t, string(accounts), `"password": "changed"`)
							groups, e := os.ReadFile(filepath.Join(root, "config/group"))
							require.NoError(t, e)
							require.Contains(t, string(groups), "team:x:2000:")
						}
					} else {
						if scenario != "no-applied" {
							next, e := os.ReadFile(filepath.Join(root, "applied.json"))
							require.NoError(t, e)
							require.Equal(t, applied, next)
						}
						if scenario != "restart-failure" {
							require.NotContains(t, r.events, "restart smbd", "unchanged/failed publication never rebuilds SAM")
						}
					}
					owner, e := os.ReadFile(filepath.Join(smbBase(), "owner.json"))
					require.NoError(t, e)
					require.Equal(t, "alpha", string(owner))
					if scenario == "stop" || scenario == "logical-deleted" {
						require.Error(t, checkSMBStartIntent(root))
					} else {
						require.NoError(t, checkSMBStartIntent(root))
					}
					if scenario == "healthy-unchanged" || scenario == "unready-unchanged" {
						require.NotContains(t, r.events, "compile")
						require.NotContains(t, r.events, "publish")
					}
					if scenario == "no-applied" || scenario == "no-service" || scenario == "stop" {
						require.Zero(t, r.reads)
						require.Empty(t, r.events)
					}
					if scenario == "unready-unchanged" || scenario == "source-failure" || scenario == "publish-failure" || scenario == "restart-failure" || scenario == "unknown-owner" {
						status, e := SMBLocalStatus(context.Background())
						require.NoError(t, e)
						require.NotEmpty(t, status.Error)
						require.NotContains(t, status.Error, "password")
						if scenario == "unready-unchanged" {
							require.False(t, status.Ready)
							require.Contains(t, status.Error, "not ready")
							require.NotContains(t, status.Error, "not confirmed applied", "health alone does not invalidate previously applied configuration")
						}
					}
					var count int
					require.NoError(t, r.db.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
					if scenario == "no-service" || scenario == "logical-deleted" {
						require.Zero(t, count)
					} else {
						require.Equal(t, 1, count)
					}
					var retained string
					require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&retained))
					require.Equal(t, ownership, retained)
					if scenario == "changed" {
						r.events = nil
						require.NoError(t, refreshSMBLocked(context.Background(), state))
						require.NotContains(t, r.events, "compile")
						require.NotContains(t, r.events, "publish")
						require.NotContains(t, r.events, "restart smbd")
					}

					if scenario == "retry-reverted" {
						if r.python != "" {
							partial, e := os.ReadFile(filepath.Join(root, "config/candidate.json"))
							require.NoError(t, e)
							require.Contains(t, string(partial), `"password": "changed"`, "exercise a real mid-publication failure, not only a process stub")
						}
						// A failed publication may have changed some live files.
						// Even source reversion must retry until locally confirmed.
						r.failure = ""
						r.changed = false
						r.events = nil
						require.NoError(t, refreshSMBLocked(context.Background(), state))
						require.Contains(t, r.events, "publish")
						require.Contains(t, r.events, "restart smbd")
						next, e := os.ReadFile(filepath.Join(root, "applied.json"))
						require.NoError(t, e)
						require.Equal(t, applied, next)
						_, e = os.Stat(filepath.Join(root, "config-update-pending"))
						require.ErrorIs(t, e, os.ErrNotExist)
					}

					if scenario == "restart-failure" {
						r.events = nil
						require.Error(t, refreshSMBLocked(context.Background(), state))
						require.Contains(t, r.events, "publish")
						require.Contains(t, r.events, "restart smbd", "failed control work remains retryable on identical sources")
						next, e := os.ReadFile(filepath.Join(root, "applied.json"))
						require.NoError(t, e)
						require.Equal(t, applied, next)
						r.failure = ""
						r.events = nil
						require.NoError(t, refreshSMBLocked(context.Background(), state))
						require.Contains(t, r.events, "publish")
						require.Contains(t, r.events, "restart smbd")
						next, e = os.ReadFile(filepath.Join(root, "applied.json"))
						require.NoError(t, e)
						require.NotEqual(t, applied, next)
						_, e = os.Stat(filepath.Join(root, "config-update-pending"))
						require.ErrorIs(t, e, os.ErrNotExist)
					}
				})
			})
		}
	}
}

func TestSMBRefreshCannotCompleteFailedInitialPlacement(t *testing.T) {
	runSMBStartup(t, "compile", true, "", func(state interfaces.StateInterface, r *smbStartupRunner) {
		r.refresh = true
		r.failure = ""
		r.events = nil
		require.NoError(t, refreshSMBLocked(context.Background(), state))
		require.Empty(t, r.events)
		require.Zero(t, r.reads)
		var count int
		require.NoError(t, r.db.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
		require.Zero(t, count)
		_, err := os.Stat(filepath.Join(r.root, "applied.json"))
		require.ErrorIs(t, err, os.ErrNotExist)
	})
}

func TestSMBExplicitDisableAndReenableAfterRefresh(t *testing.T) {
	runSMBStartup(t, "", true, "", func(state interfaces.StateInterface, r *smbStartupRunner) {
		require.NoError(t, DisableSMB(context.Background(), state, "alpha", false))
		_, err := os.Stat(filepath.Join(smbBase(), "owner.json"))
		require.ErrorIs(t, err, os.ErrNotExist)
		require.Error(t, checkSMBStartIntent(r.root))
		require.NoError(t, enableSMBLocked(context.Background(), state, "alpha", true))
		require.NoError(t, checkSMBStartIntent(r.root))
	})
}

func TestSMBStatusReportsInterruptedConfigurationPublication(t *testing.T) {
	runSMBStartup(t, "", true, "", func(state interfaces.StateInterface, r *smbStartupRunner) {
		require.NoError(t, smbAtomicWrite(filepath.Join(r.root, "config-update-pending"), []byte("pending")))
		status, err := SMBLocalStatus(context.Background())
		require.NoError(t, err)
		require.NotEmpty(t, status.Error, "a crash before error recording must not claim confirmed application")
	})
}

func TestSMBExplicitEnableConfirmsInterruptedRefresh(t *testing.T) {
	runSMBStartup(t, "", true, "", func(state interfaces.StateInterface, r *smbStartupRunner) {
		require.NoError(t, smbAtomicWrite(filepath.Join(r.root, "config-update-pending"), []byte("pending")))
		r.events = nil
		require.NoError(t, enableSMBLocked(context.Background(), state, "alpha", true))
		require.Contains(t, r.events, "publish", "unchanged source bytes do not prove partial publication was completed")
		_, err := os.Stat(filepath.Join(r.root, "config-update-pending"))
		require.ErrorIs(t, err, os.ErrNotExist)
	})
}

func TestSMBRefreshHealthFailureDoesNotReapply(t *testing.T) {
	for _, clustered := range []bool{false, true} {
		for _, revert := range []bool{false, true} {
			name := map[bool]string{true: "clustered/", false: "nonclustered/"}[clustered]
			name += map[bool]string{true: "source-reverted", false: "health-recovered"}[revert]
			t.Run(name, func(t *testing.T) {
				runSMBStartup(t, "", clustered, os.Getenv("MICROCEPH_SMB_TEST_PYTHON"), func(state interfaces.StateInterface, r *smbStartupRunner) {
					if r.python == "" {
						launch := `{"clustered":false,"smb_port":445}`
						if clustered {
							launch = `{"clustered":true,"smb_port":445}`
						}
						require.NoError(t, smbAtomicWrite(filepath.Join(r.root, "config/context.json"), []byte(launch)))
					}
					// Enforce the same configuration-only SQL boundary on every tick.
					_, err := r.db.Exec(`CREATE TRIGGER forbid_group_update BEFORE UPDATE ON service_groups BEGIN SELECT RAISE(ABORT,'forbidden owner reservation'); END;
     CREATE TRIGGER forbid_group_insert BEFORE INSERT ON service_groups BEGIN SELECT RAISE(ABORT,'forbidden group creation'); END;
     CREATE TRIGGER forbid_service_insert BEFORE INSERT ON grouped_services BEGIN SELECT RAISE(ABORT,'forbidden enabled service creation'); END;
     CREATE TRIGGER forbid_service_delete BEFORE DELETE ON grouped_services BEGIN SELECT RAISE(ABORT,'forbidden service withdrawal'); END;`)
					require.NoError(t, err)
					appliedPath := filepath.Join(r.root, "applied.json")
					pendingPath := filepath.Join(r.root, "config-update-pending")
					before, err := os.ReadFile(appliedPath)
					require.NoError(t, err)
					r.refresh = true
					r.changed = true
					r.active["smbmetrics"] = false
					r.events = nil
					require.Error(t, refreshSMBLocked(context.Background(), state))
					require.Contains(t, r.events, "compile")
					require.Contains(t, r.events, "publish")
					require.Contains(t, r.events, "restart smbd")
					require.Contains(t, r.events, "restart smbmetrics")
					require.Contains(t, r.events, "ready smbd")
					candidatePath := filepath.Join(r.root, "config/candidate.next.json")
					if r.python != "" {
						candidatePath = filepath.Join(r.root, "config/candidate.json")
					}
					published, err := os.ReadFile(candidatePath)
					require.NoError(t, err)
					require.Contains(t, string(published), `"changed"`)
					controlFingerprint, err := os.ReadFile(pendingPath)
					require.NoError(t, err)
					require.Len(t, controlFingerprint, 64, "pending records the control-issued candidate fingerprint")
					require.NotEqual(t, before, controlFingerprint)
					wantReady := []string{"ready smbd"}
					if clustered {
						wantReady = append([]string{"ready ctdbd", "ready ctdb-nodes"}, wantReady...)
					}
					for tick := 0; tick < 2; tick++ {
						r.events = nil
						require.Error(t, refreshSMBLocked(context.Background(), state))
						require.Equal(t, wantReady, r.events, "health alone must not compile, publish or re-enter smbd's account-import restart path")
						next, e := os.ReadFile(candidatePath)
						require.NoError(t, e)
						require.Equal(t, published, next)
						next, e = os.ReadFile(appliedPath)
						require.NoError(t, e)
						require.Equal(t, before, next, "runtime unreadiness cannot confirm a new fingerprint")
						next, e = os.ReadFile(pendingPath)
						require.NoError(t, e, "retain pending state until all required roles are ready")
						require.Equal(t, controlFingerprint, next)
						status, e := SMBLocalStatus(context.Background())
						require.NoError(t, e)
						require.Contains(t, status.Error, "not confirmed applied")
						require.NotContains(t, status.Error, "password")
					}
					if revert {
						// The old confirmed fingerprint is not the live candidate now.
						// Reversion is a real change, even while awaiting health.
						r.changed = false
						r.events = nil
						require.Error(t, refreshSMBLocked(context.Background(), state))
						require.Contains(t, r.events, "compile")
						require.Contains(t, r.events, "publish")
						require.Contains(t, r.events, "restart smbd")
						r.events = nil
						require.Error(t, refreshSMBLocked(context.Background(), state))
						require.Equal(t, wantReady, r.events)
					}
					r.active["smbmetrics"] = true
					r.events = nil
					require.NoError(t, refreshSMBLocked(context.Background(), state))
					require.Equal(t, append(wantReady, "ready smbmetrics"), r.events, "health recovery confirms without another restart/import")
					next, err := os.ReadFile(appliedPath)
					require.NoError(t, err)
					if revert {
						require.Equal(t, before, next)
					} else {
						require.Equal(t, controlFingerprint, next, "confirm exactly the published/control-issued candidate")
					}
					_, err = os.Stat(pendingPath)
					require.ErrorIs(t, err, os.ErrNotExist)
					status, err := SMBLocalStatus(context.Background())
					require.NoError(t, err)
					require.Empty(t, status.Error)
					require.True(t, status.Ready)
				})
			})
		}
	}
}

func TestSMBMembershipRefreshDoesNotRestartConsumers(t *testing.T) {
	python := os.Getenv("MICROCEPH_SMB_TEST_PYTHON")
	if python == "" {
		t.Skip("requires pinned real compiler bridge")
	}
	runSMBStartup(t, "", true, python, func(state interfaces.StateInterface, r *smbStartupRunner) {
		_, err := r.db.Exec(`INSERT INTO core_cluster_members VALUES(2,'b','192.0.2.2:7443');`)
		require.NoError(t, err)
		require.NoError(t, state.ClusterState().Database().Transaction(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
			_, err := database.ReserveSMBOwner(ctx, tx, "alpha", "b", true)
			return err
		}))
		_, err = r.db.Exec(`CREATE TRIGGER forbid_membership_reserve BEFORE UPDATE ON service_groups BEGIN SELECT RAISE(ABORT,'forbidden reservation'); END;`)
		require.NoError(t, err)
		r.refresh = true
		r.events = nil
		require.NoError(t, refreshSMBLocked(context.Background(), state))
		require.Contains(t, r.events, "publish")
		require.NotContains(t, r.events, "restart smbd")
		require.NotContains(t, r.events, "restart smbmetrics")
		candidate, err := os.ReadFile(filepath.Join(r.root, "config/candidate.json"))
		require.NoError(t, err)
		require.Contains(t, string(candidate), `"identity": "2"`)
		nodes, err := os.ReadFile(filepath.Join(r.root, "ctdb/nodes"))
		require.NoError(t, err)
		require.Equal(t, "192.0.2.1\n", string(nodes), "only the fixed node monitor may publish/reload live nodes")
		r.events = nil
		require.NoError(t, refreshSMBLocked(context.Background(), state))
		require.NotContains(t, r.events, "compile")
	})
}

func TestSMBMembershipPublicationRetryDoesNotRestartSamba(t *testing.T) {
	python := os.Getenv("MICROCEPH_SMB_TEST_PYTHON")
	if python == "" {
		t.Skip("requires pinned real compiler bridge")
	}
	runSMBStartup(t, "", true, python, func(state interfaces.StateInterface, r *smbStartupRunner) {
		_, err := r.db.Exec(`INSERT INTO core_cluster_members VALUES(2,'b','192.0.2.2:7443')`)
		require.NoError(t, err)
		require.NoError(t, state.ClusterState().Database().Transaction(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
			_, err := database.ReserveSMBOwner(ctx, tx, "alpha", "b", true)
			return err
		}))
		r.refresh = true
		r.failure = "publish"
		require.Error(t, refreshSMBLocked(context.Background(), state))
		r.failure = ""
		r.events = nil
		require.NoError(t, refreshSMBLocked(context.Background(), state))
		require.NotContains(t, r.events, "restart smbd", "failed membership publication must remain membership-only on retry")
	})
}

func TestSMBMembershipChangeDuringConfigHealthWaitIsSelective(t *testing.T) {
	python := os.Getenv("MICROCEPH_SMB_TEST_PYTHON")
	if python == "" {
		t.Skip("requires pinned real compiler bridge")
	}
	runSMBStartup(t, "", true, python, func(state interfaces.StateInterface, r *smbStartupRunner) {
		r.refresh = true
		r.changed = true
		r.active["smbmetrics"] = false
		require.Error(t, refreshSMBLocked(context.Background(), state))
		_, err := r.db.Exec(`INSERT INTO core_cluster_members VALUES(2,'b','192.0.2.2:7443')`)
		require.NoError(t, err)
		require.NoError(t, state.ClusterState().Database().Transaction(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
			_, err := database.ReserveSMBOwner(ctx, tx, "alpha", "b", true)
			return err
		}))
		r.events = nil
		require.Error(t, refreshSMBLocked(context.Background(), state))
		require.Contains(t, r.events, "publish")
		require.NotContains(t, r.events, "restart smbd")
		r.events = nil
		r.active["smbmetrics"] = true
		require.NoError(t, refreshSMBLocked(context.Background(), state))
		require.NotContains(t, r.events, "compile")
		require.NotContains(t, r.events, "restart smbd")
	})
}
