package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

func smbADLifecycleFixture(t *testing.T, failure string) (interfaces.StateInterface, *smbLocalStartupRunner, *int) {
	t.Helper()
	state, r := smbLocalStartupFixture(t, failure)
	c := nativeADCandidate()
	c.Spec.JoinSources = []string{"rados://.smb/alpha/join.0.json"}
	c.Spec.BindAddresses = []json.RawMessage{json.RawMessage(`{"address":"192.0.2.1"}`)}
	c.Config = json.RawMessage(`{"samba-container-config":"v0","configs":{"alpha":{"instance_name":"alpha","instance_features":["ctdb"],"globals":["domain"],"shares":[]}},"shares":{},"globals":{"domain":{"options":{"security":"ads","realm":"EXAMPLE.TEST","workgroup":"EXAMPLE","idmap config * : backend":"autorid","idmap config * : range":"2000-9999999"}}}}`)
	raw, _ := json.Marshal(map[string]any{"service_type": "smb", "service_id": "alpha", "spec": c.Spec})
	r.spec, r.config, r.logical, r.joins, r.clustered = string(raw), string(c.Config), `{"resource_type":"ceph.smb.cluster","cluster_id":"alpha","auth_mode":"active-directory","clustering":"always","domain_settings":{"realm":"EXAMPLE.TEST"}}`, string(c.Joins[0]), true
	oldProbe, oldJoin, oldPrepare, oldReady, oldNative, oldCTDB, oldUpdate := smbADProbeFunc, smbADJoinFunc, smbADPrepareFunc, smbADReadyFunc, smbReadyNativeFunc, prepareSMBCTDBFilesFunc, updateConfigFunc
	t.Cleanup(func() {
		smbADProbeFunc, smbADJoinFunc, smbADPrepareFunc, smbADReadyFunc, smbReadyNativeFunc, prepareSMBCTDBFilesFunc, updateConfigFunc = oldProbe, oldJoin, oldPrepare, oldReady, oldNative, oldCTDB, oldUpdate
	})
	updateConfigFunc = func(context.Context, interfaces.StateInterface) error { return nil }
	prepareSMBCTDBFilesFunc = func(context.Context, string, string) error { return nil }
	count := new(int)
	smbADProbeFunc = func(ctx context.Context, root string) error {
		require.True(t, r.ctdbActive, "CTDB must be ready before join")
		require.True(t, r.winbindActive, "wrapper prepares mountpoints before consuming join attempt")
		return ctx.Err()
	}
	smbADJoinFunc = func(ctx context.Context, root string) error {
		*count++
		var config database.SMBServiceGroupConfig
		var raw string
		require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
		require.NoError(t, json.Unmarshal([]byte(raw), &config))
		require.Equal(t, "1", config.AD.JoinOwner, "attempt is committed before external mutation")
		require.False(t, config.AD.Joined)
		if failure == "join" {
			return fmt.Errorf("uncertain external join")
		}
		launch, err := loadSMBLaunch(root)
		if err != nil {
			return err
		}
		rawMember, _ := json.Marshal(launch.AD)
		return smbAtomicWrite(filepath.Join(root, "ad-initializing"), rawMember)
	}
	smbADPrepareFunc = func(ctx context.Context, root string, initial bool) error {
		return setSMBPrepared(root, "winbindd", initial)
	}
	smbADReadyFunc = func(ctx context.Context, root string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if r.unready {
			return fmt.Errorf("DC unavailable")
		}
		err := advanceSMBADStartup(root, false)
		if err != nil {
			return err
		}
		return advanceSMBADStartup(root, true)
	}
	smbReadyNativeFunc = func(ctx context.Context, root, role string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		active := r.active
		if role == "ctdbd" {
			active = r.ctdbActive
		}
		if role == "winbindd" {
			active = r.winbindActive
		}
		if !active || r.unready {
			return fmt.Errorf("not ready")
		}
		if role == "winbindd" {
			return smbADReadyFunc(ctx, root)
		}
		return nil
	}
	return state, r, count
}

func TestSMBADLifecycleOneShotJoinAndRetainedEnable(t *testing.T) {
	state, r, count := smbADLifecycleFixture(t, "")
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
	require.Equal(t, 1, *count)
	require.True(t, r.active)
	var config database.SMBServiceGroupConfig
	var raw string
	require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
	require.NoError(t, json.Unmarshal([]byte(raw), &config))
	require.True(t, config.AD.Joined)
	require.True(t, config.AD.Initialized)
	require.NoError(t, DisableSMB(context.Background(), state, "alpha", false))
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
	require.Equal(t, 1, *count, "retained trust never permits a second join")
	require.Error(t, DisableSMB(context.Background(), state, "alpha", true), "final AD owner purge must refuse")
	require.FileExists(t, filepath.Join(smbRoot("alpha"), "config/candidate.json"))
	_, err := r.db.Exec(`INSERT INTO core_cluster_members VALUES(2,'b','192.0.2.2:7443')`)
	require.NoError(t, err)
	require.NoError(t, state.ClusterState().Database().Transaction(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := database.ReserveSMBADOwner(ctx, tx, "alpha", "b", config.AuthIdentity)
		return err
	}))
	replica := filepath.Join(smbRoot("alpha"), "ctdb/persistent/autorid.tdb.0")
	require.NoError(t, smbAtomicWrite(replica, []byte("retained-native-idmap")))
	require.NoError(t, DisableSMB(context.Background(), state, "alpha", true))
	require.FileExists(t, replica, "peer purge retains AD persistent identity replicas")
	require.FileExists(t, filepath.Join(smbRoot("alpha"), "diagnostics/identity-retained"))
	require.Error(t, DisableSMB(context.Background(), state, "alpha", true), "retired purge retry must not relax final/exact-owner guards")
	require.FileExists(t, replica)

}

func TestSMBADLifecycleUncertainJoinCannotRetryAfterArtifactLoss(t *testing.T) {
	state, r, count := smbADLifecycleFixture(t, "join")
	require.Error(t, enableSMBLocked(context.Background(), state, "alpha"))
	require.Equal(t, 1, *count)
	require.False(t, r.active)
	require.NoError(t, os.RemoveAll(smbRoot("alpha")))
	require.ErrorContains(t, enableSMBLocked(context.Background(), state, "alpha"), "uncertain")
	require.Equal(t, 1, *count)
	var success int
	require.NoError(t, r.db.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&success))
	require.Zero(t, success)
}

func TestSMBADLifecyclePeerCannotInitializeFirstMapping(t *testing.T) {
	state, r, _ := smbADLifecycleFixture(t, "join")
	require.Error(t, enableSMBLocked(context.Background(), state, "alpha"))
	// Complete the original actor's external success, but not idmap initialization.
	require.NoError(t, state.ClusterState().Database().Transaction(context.Background(), func(ctx context.Context, tx *sql.Tx) error { return database.CompleteSMBADJoin(ctx, tx, "alpha", "a") }))
	_, err := r.db.Exec(`INSERT INTO core_cluster_members VALUES(2,'b','192.0.2.2:7443')`)
	require.NoError(t, err)
	require.NoError(t, state.ClusterState().Database().Transaction(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		var config database.SMBServiceGroupConfig
		var raw string
		require.NoError(t, tx.QueryRow("SELECT config FROM service_groups").Scan(&raw))
		require.NoError(t, json.Unmarshal([]byte(raw), &config))
		_, err := database.ReserveSMBADOwner(ctx, tx, "alpha", "b", config.AuthIdentity)
		return err
	}))
	state.(interfaces.CephState).State.(*mocks.MockState).ClusterName = "b"
	smbADProbeFunc = func(context.Context, string) error { return nil }
	prepared := false
	smbADPrepareFunc = func(context.Context, string, bool) error { prepared = true; return nil }
	require.ErrorContains(t, prepareSMBADRuntime(context.Background(), state, smbRoot("alpha"), "alpha"), "initial AD member")
	require.False(t, prepared, "a peer must not bootstrap mapping state for an incomplete join actor")

}

func TestSMBADRefreshBootstrapAndIdentityControls(t *testing.T) {
	state, r, count := smbADLifecycleFixture(t, "")
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
	commands := len(r.commands)
	r.joins = `{"username":"not-a-joiner","password":"rotated-private"}`
	require.NoError(t, refreshSMBLocked(context.Background(), state))
	published, err := os.ReadFile(filepath.Join(smbRoot("alpha"), "config/candidate.json"))
	require.NoError(t, err)
	require.Contains(t, string(published), "rotated-private", "bootstrap source refresh must update private inputs")
	for _, command := range r.commands[commands:] {
		require.NotContains(t, command, "snapctl restart")
		require.NotContains(t, command, "snapctl stop")
	}
	require.Equal(t, 1, *count)
	r.config = strings.Replace(r.config, `"workgroup":"EXAMPLE"`, `"workgroup":"OTHER"`, 1)
	commands = len(r.commands)
	require.ErrorContains(t, refreshSMBLocked(context.Background(), state), "identity")
	for _, command := range r.commands[commands:] {
		require.NotContains(t, command, "snapctl stop")
	}
	require.Equal(t, 1, *count)
}

func TestSMBADCancellationAfterJoinAttemptCommitDoesNotExecuteJoin(t *testing.T) {
	state, r, count := smbADLifecycleFixture(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := state.ClusterState().Database().(*mocks.MockDB)
	original := db.TxFn
	db.TxFn = func(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
		err := original(ctx, fn)
		if err != nil {
			return err
		}
		var raw string
		require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
		var config database.SMBServiceGroupConfig
		require.NoError(t, json.Unmarshal([]byte(raw), &config))
		if config.AD.JoinOwner != "" && !config.AD.Joined {
			cancel()
		}
		return nil
	}
	require.Error(t, enableSMBLocked(ctx, state, "alpha"))
	require.Zero(t, *count, "cancellation immediately after SQL write-ahead commit must precede native join")
	require.False(t, r.active)
	require.ErrorContains(t, enableSMBLocked(context.Background(), state, "alpha"), "uncertain")
	require.Zero(t, *count)
}

func TestSMBADSQLCompletionFailuresNeverPublishSmbdOrRepeatJoin(t *testing.T) {
	for _, boundary := range []string{"joined", "initialized"} {
		t.Run(boundary, func(t *testing.T) {
			state, r, count := smbADLifecycleFixture(t, "")
			db := state.ClusterState().Database().(*mocks.MockDB)
			original := db.TxFn
			db.TxFn = func(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
				return original(ctx, func(ctx context.Context, tx *sql.Tx) error {
					err := fn(ctx, tx)
					if err != nil {
						return err
					}
					var raw string
					require.NoError(t, tx.QueryRow("SELECT config FROM service_groups").Scan(&raw))
					var config database.SMBServiceGroupConfig
					require.NoError(t, json.Unmarshal([]byte(raw), &config))
					fail := config.AD.Joined
					if boundary == "initialized" {
						fail = config.AD.Initialized
					}
					if fail {
						return fmt.Errorf("injected AD SQL commit failure")
					}
					return nil
				})
			}
			require.Error(t, enableSMBLocked(context.Background(), state, "alpha"))
			require.Equal(t, 1, *count)
			require.False(t, r.active)
			launch, err := loadSMBLaunchForPublication(smbRoot("alpha"))
			require.NoError(t, err)
			require.False(t, launch.Startup["smbd"].Prepared, "distinct SQL completion is required before smbd preparation")
			db.TxFn = original
			err = enableSMBLocked(context.Background(), state, "alpha")
			if boundary == "joined" {
				require.ErrorContains(t, err, "uncertain")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 1, *count)
		})
	}
}

func TestSMBADServingRefreshKeepsCTDBAndUsesDependencyOrder(t *testing.T) {
	state, r, count := smbADLifecycleFixture(t, "")
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
	start := len(r.commands)
	r.spec = strings.Replace(r.spec, `"custom_ports":null`, `"custom_ports":{"smb":1446}`, 1)
	require.NoError(t, refreshSMBLocked(context.Background(), state))
	controls := []string{}
	for _, command := range r.commands[start:] {
		if strings.HasPrefix(command, "snapctl stop") || strings.HasPrefix(command, "snapctl restart") {
			controls = append(controls, command)
		}
		require.NotContains(t, command, "accounts")
	}
	require.Equal(t, []string{"snapctl stop microceph.smbd", "snapctl stop microceph.winbindd", "snapctl restart microceph.winbindd", "snapctl restart microceph.smbd"}, controls)
	require.Equal(t, 1, *count)
}

func TestSMBADBootstrapRefreshRetainsAdmissionValidation(t *testing.T) {
	state, r, count := smbADLifecycleFixture(t, "")
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
	original, err := os.ReadFile(filepath.Join(smbRoot("alpha"), "config/candidate.json"))
	require.NoError(t, err)
	r.joins = `{"username":7,"password":"synthetic-private"}`
	require.Error(t, refreshSMBLocked(context.Background(), state), "bootstrap-only sources must still satisfy credential admission")
	after, err := os.ReadFile(filepath.Join(smbRoot("alpha"), "config/candidate.json"))
	require.NoError(t, err)
	require.Equal(t, original, after)
	require.Equal(t, 1, *count)
}

func TestSMBADRefreshPreservesDisabledWinbindIntent(t *testing.T) {
	state, r, count := smbADLifecycleFixture(t, "")
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
	r.winbindDisabled = true
	r.spec = strings.Replace(r.spec, `"custom_ports":null`, `"custom_ports":{"smb":1446}`, 1)
	start := len(r.commands)
	require.NoError(t, refreshSMBLocked(context.Background(), state))
	launch, err := loadSMBLaunch(smbRoot("alpha"))
	require.NoError(t, err)
	require.Equal(t, 445, launch.SMBPort)
	for _, command := range r.commands[start:] {
		require.NotContains(t, command, "snapctl restart")
		require.NotContains(t, command, "snapctl stop")
		require.NotContains(t, command, "snapctl start ")
	}
	require.Equal(t, 1, *count)
}

func TestSMBADRetainedEvidenceRefusesLostSQLAuthority(t *testing.T) {
	for _, evidence := range []string{"all", "owner", "candidate", "context", "applied", "initializing", "native"} {
		t.Run(evidence, func(t *testing.T) {
			state, r, count := smbADLifecycleFixture(t, "")
			require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
			root := smbRoot("alpha")
			if evidence != "all" {
				require.NoError(t, os.RemoveAll(root))
				require.NoError(t, os.Remove(filepath.Join(smbBase(), "owner.json")))
				path := map[string]string{"owner": "../owner.json", "candidate": "config/candidate.json", "context": "config/context.json", "applied": "applied.json", "initializing": "ad-initializing", "native": "ctdb/persistent/autorid.tdb.0"}[evidence]
				data := []byte("retained evidence")
				if evidence == "owner" {
					data = []byte("alpha")
				}
				require.NoError(t, smbAtomicWrite(filepath.Join(root, path), data))
			}
			_, err := r.db.Exec("DELETE FROM grouped_services; DELETE FROM service_groups")
			require.NoError(t, err)
			start := len(r.commands)
			err = enableSMBLocked(context.Background(), state, "alpha")
			require.Error(t, err, "retained local evidence must not authorize SQL reconstruction or no-op success")
			var groups int
			require.NoError(t, r.db.QueryRow("SELECT COUNT(*) FROM service_groups").Scan(&groups))
			require.Zero(t, groups, "refusal must not replace lost authority")
			require.Equal(t, 1, *count)
			for _, command := range r.commands[start:] {
				require.NotContains(t, command, "snapctl")
				require.NotContains(t, command, "client.smb.fs.")
			}
		})
	}
}

func TestSMBADNoOpRequiresConfirmedExactSQLHistory(t *testing.T) {
	for _, loss := range []string{"initialization", "attempt", "success-row"} {
		t.Run(loss, func(t *testing.T) {
			state, r, count := smbADLifecycleFixture(t, "")
			require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
			if loss == "success-row" {
				_, err := r.db.Exec("DELETE FROM grouped_services")
				require.NoError(t, err)
			} else {
				var raw string
				require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
				var config database.SMBServiceGroupConfig
				require.NoError(t, json.Unmarshal([]byte(raw), &config))
				config.AD.Initialized = false
				if loss == "attempt" {
					config.AD = &database.SMBADState{}
				}
				data, err := json.Marshal(config)
				require.NoError(t, err)
				_, err = r.db.Exec("UPDATE service_groups SET config=?", string(data))
				require.NoError(t, err)
			}
			var before string
			require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&before))
			probes := 0
			originalReady := smbReadyNativeFunc
			smbReadyNativeFunc = func(ctx context.Context, root, role string) error {
				probes++
				return originalReady(ctx, root, role)
			}
			start := len(r.commands)
			require.Error(t, enableSMBLocked(context.Background(), state, "alpha"))
			require.Zero(t, probes, "exact SQL history must be checked before native readiness/mapping requests")
			var after string
			require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&after))
			require.Equal(t, before, after)
			require.Equal(t, 1, *count)
			for _, command := range r.commands[start:] {
				require.NotContains(t, command, "snapctl")
			}
		})
	}
}

func TestSMBADIncompleteInitializationRefusesMissingContext(t *testing.T) {
	for _, marker := range []bool{true, false} {
		t.Run(fmt.Sprint(marker), func(t *testing.T) {
			state, r, count := smbADLifecycleFixture(t, "")
			ready := smbADReadyFunc
			smbADReadyFunc = func(ctx context.Context, root string) error {
				require.NoError(t, advanceSMBADStartup(root, false))
				return fmt.Errorf("allocating response lost")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			require.Error(t, enableSMBLocked(ctx, state, "alpha"))
			require.Equal(t, 1, *count)
			root := smbRoot("alpha")
			require.NoError(t, os.Remove(filepath.Join(root, "config/context.json")))
			if !marker {
				require.NoError(t, os.Remove(filepath.Join(root, "ad-initializing")))
			}
			smbADReadyFunc = ready
			var before string
			require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&before))
			start := len(r.commands)
			require.Error(t, enableSMBLocked(context.Background(), state, "alpha"))
			require.NoFileExists(t, filepath.Join(root, "config/context.json"))
			var after string
			require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&after))
			require.Equal(t, before, after)
			require.Equal(t, 1, *count)
			for _, command := range r.commands[start:] {
				require.NotContains(t, command, "snapctl", "refuse before restarting/preparing any native role")
			}
		})
	}
}

func TestSMBADServingRefreshWaitsForWinbindReadiness(t *testing.T) {
	for _, outcome := range []string{"delayed", "canceled", "exhausted"} {
		t.Run(outcome, func(t *testing.T) {
			state, r, count := smbADLifecycleFixture(t, "")
			require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
			ready := smbADReadyFunc
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if outcome == "exhausted" {
				ctx, cancel = context.WithTimeout(context.Background(), 1200*time.Millisecond)
				defer cancel()
			}
			probes := 0
			var firstDeadline time.Time
			smbADReadyFunc = func(probeCtx context.Context, root string) error {
				probes++
				require.False(t, r.active, "smbd must remain stopped until Winbind is ready")
				require.True(t, r.winbindActive)
				deadline, bounded := probeCtx.Deadline()
				require.True(t, bounded)
				if probes == 1 {
					firstDeadline = deadline
				} else {
					require.Equal(t, firstDeadline, deadline, "retries must not reset the wait/work budget")
				}
				if outcome == "canceled" {
					cancel()
					return nil // A success racing cancellation must not start smbd.
				}
				if outcome == "exhausted" || probes < 3 {
					return fmt.Errorf("Winbind wrapper is still starting")
				}
				return ready(probeCtx, root)
			}
			r.onControl = func(controlCtx context.Context, action string) error {
				if action == "restart" {
					require.Equal(t, "delayed", outcome)
					require.Equal(t, 3, probes, "start smbd only after the successful readiness probe")
				}
				if action == "stop" {
					require.NoError(t, controlCtx.Err(), "compensation must detach from caller cancellation")
				}
				return nil
			}
			start := len(r.commands)
			r.spec = strings.Replace(r.spec, `"custom_ports":null`, `"custom_ports":{"smb":1446}`, 1)
			err := refreshSMBLocked(ctx, state)
			controls := []string{}
			for _, command := range r.commands[start:] {
				if strings.HasPrefix(command, "snapctl stop") || strings.HasPrefix(command, "snapctl restart") {
					controls = append(controls, command)
				}
				require.NotContains(t, command, "--enable", "refresh cannot enable disabled roles")
			}
			expected := []string{"snapctl stop microceph.smbd", "snapctl stop microceph.winbindd", "snapctl restart microceph.winbindd"}
			if outcome == "delayed" {
				require.NoError(t, err)
				require.Equal(t, 3, probes)
				expected = append(expected, "snapctl restart microceph.smbd")
				require.True(t, r.active)
				require.True(t, r.winbindActive)
			} else {
				require.ErrorIs(t, err, ctx.Err())
				if outcome == "exhausted" {
					require.GreaterOrEqual(t, probes, 2, "transient failure must wait until the shared deadline")
				}
				expected = append(expected, "snapctl stop microceph.smbd", "snapctl stop microceph.winbindd")
				require.False(t, r.active)
				require.False(t, r.winbindActive)
			}
			require.Equal(t, expected, controls, "one restart only, with ordered readiness/compensation")
			require.True(t, r.ctdbActive, "refresh leaves CTDB running")
			require.Equal(t, 1, *count)
		})
	}
}

func TestSMBADConfirmedEnableNoOpKeepsNativeRoles(t *testing.T) {
	state, r, count := smbADLifecycleFixture(t, "")
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
	start := len(r.commands)
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
	require.Equal(t, 1, *count)
	for _, command := range r.commands[start:] {
		require.NotContains(t, command, "snapctl", "confirmed healthy no-op must not control native roles")
	}
	require.True(t, r.active)
	require.True(t, r.winbindActive)
}

func TestSMBADFreshPeerAttachesWithoutInitialAllocationPermission(t *testing.T) {
	state, r, count := smbADLifecycleFixture(t, "")
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
	_, err := r.db.Exec(`INSERT INTO core_cluster_members VALUES(2,'b','192.0.2.2:7443')`)
	require.NoError(t, err)
	// Model a second snap instance sharing only SQL/CTDB authority. It has no
	// local publication, owner file or join marker from the initial member.
	t.Setenv("SNAP_COMMON", t.TempDir())
	state.(interfaces.CephState).State.(*mocks.MockState).ClusterName = "b"
	common.Network = smbEndpointNetwork{addresses: []string{"192.0.2.2"}}
	r.spec = strings.Replace(r.spec, `"address":"192.0.2.1"`, `"address":"192.0.2.2"`, 1)
	r.active, r.ctdbActive, r.winbindActive = false, false, false
	prepared := 0
	smbADPrepareFunc = func(ctx context.Context, root string, initial bool) error {
		prepared++
		require.False(t, initial, "an initialized peer must require existing trust/idmap records")
		return setSMBPrepared(root, "winbindd", initial)
	}
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
	require.Equal(t, 1, prepared)
	require.Equal(t, 1, *count, "peer attachment must never execute a second join")
	require.True(t, r.active)
	require.True(t, r.winbindActive)
	require.NoFileExists(t, filepath.Join(smbRoot("alpha"), "ad-initializing"))
	require.NoError(t, state.ClusterState().Database().Transaction(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		return database.GetSMBConfiguredService(ctx, tx, "alpha", "b")
	}))
}
