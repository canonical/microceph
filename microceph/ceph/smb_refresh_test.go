package ceph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/interfaces"
)

func refreshFixture(t *testing.T) (interfaces.StateInterface, *smbLocalStartupRunner) {
	t.Helper()
	state, r := smbLocalStartupFixture(t, "")
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
	// Any attempted placement/identity mutation must fail, including idempotent reservation.
	_, err := r.db.Exec(`CREATE TRIGGER no_group_update BEFORE UPDATE ON service_groups BEGIN SELECT RAISE(ABORT,'owner/history mutation'); END;
 CREATE TRIGGER no_group_insert BEFORE INSERT ON service_groups BEGIN SELECT RAISE(ABORT,'owner creation'); END;
 CREATE TRIGGER no_service_insert BEFORE INSERT ON grouped_services BEGIN SELECT RAISE(ABORT,'placement creation'); END;`)
	require.NoError(t, err)
	r.commands = nil
	return state, r
}

func assertNoRefreshEffects(t *testing.T, r *smbLocalStartupRunner) {
	t.Helper()
	for _, cmd := range r.commands {
		require.NotContains(t, cmd, " accounts ")
		require.NotContains(t, cmd, " render ")
		require.NotContains(t, cmd, "snapctl restart")
		require.NotContains(t, cmd, "snapctl stop")
		require.NotContains(t, cmd, "snapctl start")
		require.NotContains(t, cmd, " auth ")
		require.NotContains(t, cmd, " mgr ")
	}
}

func changeRefreshSource(r *smbLocalStartupRunner) {
	r.spec = strings.Replace(r.spec, `"bind_addrs":`, `"custom_ports":{"smb":2445},"bind_addrs":`, 1)
	r.config = strings.Replace(r.config, `"globals":{}`, `"globals":{"changed":{"options":{"server string":"updated"}}}`, 1)
	r.config = strings.Replace(r.config, `"globals":[]`, `"globals":["changed"]`, 1)
}

func TestSMBRefreshAdoptsPublishedParentNotFreshSources(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "already-changed"}[changed], func(t *testing.T) {
			state, r := refreshFixture(t)
			root := smbRoot("alpha")
			before, err := os.ReadFile(filepath.Join(root, "applied.json"))
			require.NoError(t, err)
			require.NoError(t, os.Remove(filepath.Join(root, "applied.json"))) // exact PR1 marker absence
			if changed {
				changeRefreshSource(r)
			}
			require.NoError(t, refreshSMBLocked(context.Background(), state))
			after, err := os.ReadFile(filepath.Join(root, "applied.json"))
			require.NoError(t, err)
			if changed {
				require.NotEqual(t, before, after)
				require.Contains(t, strings.Join(r.commands, "\n"), "snapctl restart microceph.smbd")
				launch, err := loadSMBLaunch(root)
				require.NoError(t, err)
				require.Equal(t, 2445, launch.SMBPort)
			} else {
				require.Equal(t, before, after)
				assertNoRefreshEffects(t, r)
			}
			sam, err := os.ReadFile(filepath.Join(root, "samba/private/secrets.tdb"))
			require.NoError(t, err)
			require.Equal(t, "retained-native-identity", string(sam))
			r.commands = nil
			require.NoError(t, refreshSMBLocked(context.Background(), state))
			assertNoRefreshEffects(t, r)
		})
	}
}

func TestSMBRefreshIneligibleCannotInitializeOrRecover(t *testing.T) {
	for _, scenario := range []string{"no-service", "retired", "stop", "disabled", "no-history", "missing-sam", "missing-fence", "unprepared-parent", "missing-candidate", "incomplete-parent", "contradictory-parent", "unknown-owner"} {
		t.Run(scenario, func(t *testing.T) {
			state, r := refreshFixture(t)
			root := smbRoot("alpha")
			switch scenario {
			case "no-service":
				_, err := r.db.Exec("DELETE FROM grouped_services")
				require.NoError(t, err)
			case "retired":
				require.NoError(t, os.Remove(filepath.Join(smbBase(), "owner.json")))
			case "stop":
				require.NoError(t, smbAtomicWrite(filepath.Join(root, "stop-requested"), []byte("stop")))
			case "disabled":
				r.disabled = true
			case "no-history":
				_, err := r.db.Exec(`DROP TRIGGER no_group_update; UPDATE service_groups SET config=replace(config,'"local_sam_started":{"1":"a"}','"local_sam_started":{}')`)
				require.NoError(t, err)
			case "missing-sam":
				require.NoError(t, os.Remove(filepath.Join(root, "samba/private/secrets.tdb")))
			case "missing-fence":
				require.NoError(t, os.Remove(filepath.Join(root, "identity/local-sam")))
			case "unprepared-parent":
				require.NoError(t, os.Remove(filepath.Join(root, "applied.json")))
				launch, err := loadSMBLaunch(root)
				require.NoError(t, err)
				launch.Startup = smbDefaultStartup()
				require.NoError(t, writeSMBLaunchState(root, launch))
			case "missing-candidate":
				require.NoError(t, os.Remove(filepath.Join(root, "config/candidate.json")))
			case "incomplete-parent", "contradictory-parent":
				require.NoError(t, os.Remove(filepath.Join(root, "applied.json")))
				raw, err := os.ReadFile(filepath.Join(root, "config/candidate.json"))
				require.NoError(t, err)
				if scenario == "incomplete-parent" {
					raw = []byte(strings.Replace(string(raw), `"spec_document":{`, `"spec_document":{},"ignored":{`, 1))
				} else {
					raw = []byte(strings.Replace(string(raw), `"smb_port":445`, `"smb_port":2445`, 1))
				}
				require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/candidate.json"), raw))
			case "unknown-owner":
				_, err := r.db.Exec(`DROP TRIGGER no_group_update; UPDATE service_groups SET config=replace(config,'"owners":{"1":"a"}','"owners":{}')`)
				require.NoError(t, err)
			}
			changeRefreshSource(r)
			err := refreshSMBLocked(context.Background(), state)
			if scenario == "no-service" || scenario == "retired" || scenario == "stop" || scenario == "disabled" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			assertNoRefreshEffects(t, r)
		})
	}
}

func TestSMBRefreshRefusesUnsafeInputsBeforePublication(t *testing.T) {
	for _, scenario := range []string{"clustered", "domain", "proxy", "include", "source-change", "source-failure", "compile", "logical-unknown"} {
		t.Run(scenario, func(t *testing.T) {
			state, r := refreshFixture(t)
			root := smbRoot("alpha")
			before, err := os.ReadFile(filepath.Join(root, "config/candidate.json"))
			require.NoError(t, err)
			switch scenario {
			case "clustered", "domain", "proxy":
				r.spec = strings.Replace(r.spec, `"spec":{`, `"spec":{"features":["`+scenario+`"],`, 1)
			case "include":
				r.config = strings.Replace(r.config, `"globals":{}`, `"globals":{"bad":{"options":{"include":"/tmp/mutable"}}}`, 1)
				r.config = strings.Replace(r.config, `"globals":[]`, `"globals":["bad"]`, 1)
			case "logical-unknown":
				r.logical = `{"unexpected":true}`
			default:
				changeRefreshSource(r)
				r.failure = scenario
			}
			require.Error(t, refreshSMBLocked(context.Background(), state))
			after, err := os.ReadFile(filepath.Join(root, "config/candidate.json"))
			require.NoError(t, err)
			require.Equal(t, before, after)
			for _, cmd := range r.commands {
				require.NotContains(t, cmd, " accounts ")
				require.NotContains(t, cmd, "snapctl restart")
				require.NotContains(t, cmd, "snapctl stop")
			}
		})
	}
}

func TestSMBRefreshInterruptedPublicationRetriesEvenAfterReversion(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "pre-publication", true: "mid-publication"}[partial], func(t *testing.T) {
			state, r := refreshFixture(t)
			root := smbRoot("alpha")
			oldSpec, oldConfig := r.spec, r.config
			changeRefreshSource(r)
			old := publishSMBNativeFunc
			t.Cleanup(func() { publishSMBNativeFunc = old })
			publishSMBNativeFunc = func(ctx context.Context, root string) error {
				if partial {
					// Exercise the real context-first publisher, then a later live-file failure.
					path := filepath.Join(root, "config/smb.conf")
					require.NoError(t, os.Remove(path))
					require.NoError(t, os.Symlink("/never-write", path))
					err := old(ctx, root)
					require.Error(t, err)
					launch, e := loadSMBLaunch(root)
					require.NoError(t, e)
					require.False(t, launch.Startup["smbd"].Prepared)
					require.NoError(t, os.Remove(path))
				}
				return errors.New("publication failed")
			}
			require.Error(t, refreshSMBLocked(context.Background(), state))
			require.False(t, r.active)
			require.NoError(t, checkSMBStartIntent(root), "background containment is not user disable")
			publishSMBNativeFunc = old
			r.spec, r.config = oldSpec, oldConfig
			r.commands = nil
			require.NoError(t, refreshSMBLocked(context.Background(), state))
			require.Contains(t, strings.Join(r.commands, "\n"), "snapctl restart microceph.smbd")
			_, err := os.Stat(filepath.Join(root, "config-update-pending"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestSMBRefreshCompletedControlsOnlyObserveHealth(t *testing.T) {
	state, r := refreshFixture(t)
	changeRefreshSource(r)
	r.unready = true
	require.Error(t, refreshSMBLocked(context.Background(), state))
	require.Contains(t, strings.Join(r.commands, "\n"), "snapctl restart microceph.smbd")
	for i := 0; i < 2; i++ {
		r.commands = nil
		require.Error(t, refreshSMBLocked(context.Background(), state))
		assertNoRefreshEffects(t, r)
	}
	status, err := SMBLocalStatus(context.Background(), state)
	require.NoError(t, err)
	require.Contains(t, status.Error, "not confirmed applied")
	r.unready = false
	r.commands = nil
	require.NoError(t, refreshSMBLocked(context.Background(), state))
	assertNoRefreshEffects(t, r)
	r.active = false
	r.commands = nil
	require.Error(t, refreshSMBLocked(context.Background(), state))
	assertNoRefreshEffects(t, r)
	status, err = SMBLocalStatus(context.Background(), state)
	require.NoError(t, err)
	require.Contains(t, status.Error, "not ready")
}

func TestSMBRefreshControlFailureAndCancellationRetryWithoutDisable(t *testing.T) {
	for _, failure := range []string{"restart", "accounts", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			state, r := refreshFixture(t)
			changeRefreshSource(r)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failure == "cancel" {
				r.onControl = func(ctx context.Context, op string) error {
					if op == "restart" {
						cancel()
						return ctx.Err()
					}
					return nil
				}
			} else {
				r.failure = failure
			}
			require.Error(t, refreshSMBLocked(ctx, state))
			require.False(t, r.active)
			require.False(t, r.disabled)
			require.NoError(t, checkSMBStartIntent(smbRoot("alpha")))
			for _, cmd := range r.commands {
				require.NotContains(t, cmd, "--enable")
				require.NotContains(t, cmd, "--disable")
			}
			r.failure = ""
			r.onControl = nil
			r.commands = nil
			require.NoError(t, refreshSMBLocked(context.Background(), state))
			require.Contains(t, strings.Join(r.commands, "\n"), "snapctl restart microceph.smbd")
		})
	}
}

func TestSMBRefreshFailedFirstEnableIsNotBootstrap(t *testing.T) {
	state, r := smbLocalStartupFixture(t, "accounts")
	require.Error(t, enableSMBLocked(context.Background(), state, "alpha"))
	r.failure = ""
	r.commands = nil
	require.NoError(t, refreshSMBLocked(context.Background(), state))
	assertNoRefreshEffects(t, r)
}

func TestSMBRefreshChangesUsersSharesAndEndpointRetainingIdentity(t *testing.T) {
	state, r := refreshFixture(t)
	root := smbRoot("alpha")
	key, err := os.ReadFile(filepath.Join(root, "identity/data.keyring"))
	require.NoError(t, err)
	candidate := nativeTestCandidate()
	r.config = string(candidate.Config)
	r.users = string(candidate.Users[0])
	r.spec = strings.Replace(r.spec, `"config_uri":`, `"user_sources":["rados://.smb/alpha/users.json"],"custom_ports":{"smb":2445},"config_uri":`, 1)
	require.NoError(t, refreshSMBLocked(context.Background(), state))
	conf, err := os.ReadFile(filepath.Join(root, "config/smb.conf"))
	require.NoError(t, err)
	require.Contains(t, string(conf), "[data]")
	require.Contains(t, string(conf), "smb ports = 2445")
	passwd, err := os.ReadFile(filepath.Join(root, "config/passwd"))
	require.NoError(t, err)
	require.Contains(t, string(passwd), "alice:x:0:3000:")
	r.users = strings.Replace(r.users, `"password":"secret"`, `"password":"changed"`, 1)
	r.users = strings.Replace(r.users, `,{"name":"bob","password":"other"}`, "", 1)
	require.NoError(t, refreshSMBLocked(context.Background(), state))
	passwd, err = os.ReadFile(filepath.Join(root, "config/passwd"))
	require.NoError(t, err)
	require.NotContains(t, string(passwd), "bob:")
	after, err := os.ReadFile(filepath.Join(root, "identity/data.keyring"))
	require.NoError(t, err)
	require.Equal(t, key, after)
	for _, name := range []string{"secrets.tdb", "passdb.tdb"} {
		data, err := os.ReadFile(filepath.Join(root, "samba/private", name))
		require.NoError(t, err)
		require.Equal(t, "retained-native-identity", string(data))
	}
}

func TestSMBRefreshCompletedControlsSourceReversionIsARealChange(t *testing.T) {
	state, r := refreshFixture(t)
	oldSpec, oldConfig := r.spec, r.config
	changeRefreshSource(r)
	r.unready = true
	require.Error(t, refreshSMBLocked(context.Background(), state))
	r.spec, r.config = oldSpec, oldConfig
	r.commands = nil
	require.Error(t, refreshSMBLocked(context.Background(), state))
	require.Contains(t, strings.Join(r.commands, "\n"), "snapctl restart microceph.smbd")
	r.commands = nil
	r.unready = false
	require.NoError(t, refreshSMBLocked(context.Background(), state))
	assertNoRefreshEffects(t, r)
}

func TestSMBRefreshDisableReenableAndConfirmedDeletion(t *testing.T) {
	state, r := smbLocalStartupFixture(t, "")
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
	changeRefreshSource(r)
	require.NoError(t, refreshSMBLocked(context.Background(), state))
	require.NoError(t, DisableSMB(context.Background(), state, "alpha", false))
	r.commands = nil
	require.NoError(t, refreshSMBLocked(context.Background(), state))
	assertNoRefreshEffects(t, r)
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
	r.commands = nil
	require.NoError(t, refreshSMBLocked(context.Background(), state))
	assertNoRefreshEffects(t, r)
	r.logical = `{"resources":[]}`
	require.Error(t, refreshSMBLocked(context.Background(), state))
	require.False(t, r.active)
	require.Error(t, checkSMBStartIntent(smbRoot("alpha")))
	var count int
	require.NoError(t, r.db.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
	require.Zero(t, count)
	var raw string
	require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
	require.Contains(t, raw, `"local_sam_started":{"1":"a"}`)
}

func TestSMBRefreshEquivalentJSONIsNoOp(t *testing.T) {
	state, r := refreshFixture(t)
	// Upstream reserialization alone is not a configuration change.
	r.config = strings.Replace(r.config, `"globals":{},"shares":{}`, `"shares":{},"globals":{}`, 1)
	require.NoError(t, refreshSMBLocked(context.Background(), state))
	assertNoRefreshEffects(t, r)
}

func TestSMBRefreshLogicalModeChangeDuringPreparationIsRefused(t *testing.T) {
	state, r := refreshFixture(t)
	changeRefreshSource(r)
	old := compileSMBNativeFunc
	t.Cleanup(func() { compileSMBNativeFunc = old })
	compileSMBNativeFunc = func(ctx context.Context, root string, c smbCandidate) error {
		err := old(ctx, root, c)
		r.logical = `{"resource_type":"ceph.smb.cluster","cluster_id":"alpha","auth_mode":"active-directory","clustering":"never"}`
		return err
	}
	require.Error(t, refreshSMBLocked(context.Background(), state))
	for _, cmd := range r.commands {
		require.NotContains(t, cmd, "snapctl stop")
		require.NotContains(t, cmd, "snapctl restart")
		require.NotContains(t, cmd, " accounts ")
	}
}
