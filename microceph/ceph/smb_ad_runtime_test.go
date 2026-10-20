package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

func TestSMBADRuntimeOrderingAndNoSAM(t *testing.T) {
	for _, role := range []string{"smbd", "winbindd"} {
		t.Run(role, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "alpha")
			require.NoError(t, smbAtomicWrite(filepath.Join(base, "owner.json"), []byte("alpha")))
			raw, _ := json.Marshal(smbLaunch{Clustered: true, CTDBPNN: new(uint64), AD: &smbADMember{Realm: "EXAMPLE.TEST"}})
			require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), raw))
			events := []string{}
			ops := smbRuntimeOps{
				enter:     func(smbRolePlan) error { events = append(events, "namespace"); return nil },
				ctdbReady: func(context.Context, string) error { events = append(events, "ctdb"); return nil },
				member:    func(context.Context, string, bool) error { events = append(events, "member"); return nil },
				accounts:  func(context.Context, string) error { t.Fatal("AD must never import SAM"); return nil },
				exec:      func(smbRolePlan) error { events = append(events, "exec"); return nil },
			}
			require.NoError(t, launchSMBNative(context.Background(), root, role, ops))
			want := []string{"namespace", "ctdb", "member", "exec"}
			if role == "winbindd" {
				want = []string{"namespace", "ctdb", "exec"}
			}
			require.Equal(t, want, events)
			if role == "winbindd" {
				return
			}
			ops.member = func(context.Context, string, bool) error { return errors.New("lost state") }
			events = nil
			require.ErrorContains(t, launchSMBNative(context.Background(), root, role, ops), "lost state")
			require.Equal(t, []string{"namespace", "ctdb"}, events)
		})
	}
	c := nativeADCandidate()
	roles, err := smbCoreRoles(c.Spec, true)
	require.NoError(t, err)
	require.Equal(t, []string{"ctdbd", "ctdb-nodes", "cephfs-proxy", "winbindd", "smbd", "smbmetrics"}, roles)
	require.NoError(t, validateSMBLogicalPolicy(c.Logical, c.Spec))
	c.Spec.Features = []string{"domain"}
	require.Error(t, validateSMBLogicalPolicy(c.Logical, c.Spec))
}

func TestSMBADBoundedProcessKillsChildAndProtectsSecrets(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child")
	script := filepath.Join(dir, "probe")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho private-password >&2\nsleep 60 &\necho $! > \"$1\"\nwait\n"), 0700))
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runSMBADProcess(ctx, dir, []string{script, child}, nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-password")
	require.Less(t, time.Since(started), 3*time.Second)
	raw, err := os.ReadFile(child)
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	// A killed child can be a zombie until its namespace init reaps it.
	raw, err = os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err == nil {
		require.Contains(t, string(raw), ") Z ", "descendant must not remain executing")
	}
	info, err := os.Stat(filepath.Join(dir, "diagnostics/ad-process-error"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	raw, err = os.ReadFile(filepath.Join(dir, "diagnostics/ad-process-error"))
	require.NoError(t, err)
	require.Contains(t, string(raw), "private-password")
}

func TestSMBADJoinOnlyFingerprintDoesNotRestart(t *testing.T) {
	c := nativeADCandidate()
	old := c.configurationFingerprint()
	full := c.fingerprint()
	c.Joins[0] = json.RawMessage(`{"username":"new","password":"new-private"}`)
	c.Spec.JoinSources[0] = "rados:mon-config-key:smb/config/alpha/replacement"
	c.SpecBytes = json.RawMessage(`{"changed":"join input"}`)
	require.Equal(t, old, c.configurationFingerprint())
	require.NotEqual(t, full, c.fingerprint())
	c.Config = json.RawMessage(strings.Replace(string(c.Config), "EXAMPLE.TEST", "OTHER.TEST", 1))
	require.NotEqual(t, old, c.configurationFingerprint())
}

func TestSMBADMountTargetRefusesForeignState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "winbindd")
	require.NoError(t, prepareSMBCTDBMountpoint(path, uint32(os.Geteuid())))
	require.NoError(t, os.WriteFile(filepath.Join(path, "pipe"), []byte("foreign"), 0600))
	require.Error(t, prepareSMBCTDBMountpoint(path, uint32(os.Geteuid())))
}

func TestSMBADPrivateSocketCancelsActorOnDisconnect(t *testing.T) {
	root := filepath.Join(t.TempDir(), "alpha")
	require.NoError(t, smbPrivateDir(filepath.Join(root, "samba/run")))
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	listener, guard, err := openSMBPrivateListener(filepath.Join(root, "samba/run/native-ad.socket"))
	require.NoError(t, err)
	defer guard.Close()
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- serveSMBADControls(ctx, listener, func(ctx context.Context, op byte) error {
			require.Equal(t, byte('J'), op)
			close(entered)
			<-ctx.Done()
			close(cancelled)
			return ctx.Err()
		})
	}()
	conn, err := net.Dial("unix", listener.Addr().String())
	require.NoError(t, err)
	_, err = conn.Write([]byte("J"))
	require.NoError(t, err)
	<-entered
	require.NoError(t, conn.Close())
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("join must cancel when daemon disconnects")
	}
	cancel()
	require.NoError(t, <-done)
	info, err := os.Stat(listener.Addr().String())
	if err == nil {
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
}

func TestSMBADStartupRefreshRestartAndRetirement(t *testing.T) {
	python := os.Getenv("MICROCEPH_SMB_TEST_PYTHON")
	if python == "" {
		t.Skip("requires pinned real bindings")
	}
	c := nativeADCandidate()
	c.Spec.CustomPorts = map[string]int{"smb": 1445, "smbmetrics": 19922}
	c.Spec.ConfigURI = "rados://.smb/alpha/config.smb"
	var document map[string]any
	require.NoError(t, json.Unmarshal(c.Config, &document))
	document["shares"] = map[string]any{}
	document["configs"].(map[string]any)["alpha"].(map[string]any)["shares"] = []any{}
	c.Config, _ = json.Marshal(document)
	runSMBStartupFixture(t, "", true, python, &c, func(state interfaces.StateInterface, r *smbStartupRunner) {
		var config database.SMBServiceGroupConfig
		read := func() {
			var raw string
			require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
			config = database.SMBServiceGroupConfig{}
			require.NoError(t, json.Unmarshal([]byte(raw), &config))
		}
		read()
		require.True(t, config.AD.Joined)
		require.True(t, config.AD.Initialized)
		r.refresh = true
		r.events = nil
		require.NoError(t, refreshSMBLocked(context.Background(), state))
		require.NotContains(t, r.events, "compile")
		require.NotContains(t, r.events, "ad join")
		c.Joins[0] = json.RawMessage(`{"username":"changed","password":"changed-private"}`)
		r.events = nil
		require.NoError(t, refreshSMBLocked(context.Background(), state))
		require.Contains(t, r.events, "publish")
		require.NotContains(t, r.events, "restart winbindd")
		require.NotContains(t, r.events, "restart smbd")
		require.NotContains(t, r.events, "ad join")
		saved, err := os.ReadFile(filepath.Join(r.root, "config/candidate.json"))
		require.NoError(t, err)
		require.Contains(t, string(saved), "changed-private")
		r.refresh = false
		r.events = nil
		c.Joins[0] = json.RawMessage(`{"username":"second-refresh","password":"second-private"}`)
		require.NoError(t, enableSMBLocked(context.Background(), state, "alpha", true))
		require.NotContains(t, r.events, "start winbindd")
		require.NotContains(t, r.events, "stop smbd")
		require.NotContains(t, r.events, "ad join")
		r.refresh = true
		original := c.Config
		c.Config = json.RawMessage(strings.Replace(string(c.Config), `"workgroup":"EXAMPLE"`, `"workgroup":"SHORT"`, 1))
		r.events = nil
		require.ErrorContains(t, refreshSMBLocked(context.Background(), state), "identity changed")
		require.NotContains(t, r.events, "publish")
		require.NotContains(t, r.events, "ad join")
		c.Config = original

		// A short DC outage is observation only; never another join/reset/restart.
		r.unready = true
		r.events = nil
		require.Error(t, refreshSMBLocked(context.Background(), state))
		require.NotContains(t, r.events, "publish")
		require.NotContains(t, r.events, "ad join")
		r.unready = false
		r.refresh = false
		r.events = nil
		require.NoError(t, DisableSMB(context.Background(), state, "alpha", false))
		require.NoError(t, enableSMBLocked(context.Background(), state, "alpha", true))
		require.NotContains(t, r.events, "ad join")
		read()
		require.True(t, config.AD.Joined)
		require.Equal(t, "1", config.AD.JoinOwner)
		require.ErrorContains(t, DisableSMB(context.Background(), state, "alpha", true), "final")
		// A peer purge never deletes the retained local replica, peer resource or trust.
		_, err = r.db.Exec(`INSERT INTO core_cluster_members VALUES(2,'b','192.0.2.2:7443')`)
		require.NoError(t, err)
		require.NoError(t, state.ClusterState().Database().Transaction(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
			_, e := database.ReserveSMBADOwner(ctx, tx, "alpha", "b", config.AuthIdentity)
			return e
		}))
		require.NoError(t, writeSMBNativeFile(filepath.Join(r.root, "ctdb/persistent/autorid.tdb.0"), []byte("retain-idmap")))
		require.NoError(t, DisableSMB(context.Background(), state, "alpha", true))
		require.FileExists(t, filepath.Join(r.root, "ctdb/persistent/autorid.tdb.0"))
		read()
		require.Equal(t, map[string]string{"2": "b"}, config.Owners)
		require.True(t, config.AD.Initialized)
	})
}

func smbADProcessFixture(t *testing.T) (string, string) {
	t.Helper()
	base, err := os.MkdirTemp("", "smb-ad-process-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	root := filepath.Join(base, "alpha")
	snap := t.TempDir()
	t.Setenv("SNAP", snap)
	require.NoError(t, os.MkdirAll(filepath.Join(snap, "bin"), 0700))
	// Samba secrets uses string_tdb_data (no NUL); autorid CONFIG uses
	// string_term_tdb_data. The CTDB CLI decodes \\00, it does not add it.
	script := `#!/bin/sh
[ "$5" = pfetch ] || exit 7
case "$6:$7" in
 secrets.tdb:SECRETS/MACHINE_PASSWORD/EXAMPLE|secrets.tdb:SECRETS/SID/EXAMPLE) printf 'retained\n';;
 'autorid.tdb:CONFIG\00') [ ! -e "$SNAP/missing-idmap" ] && printf 'minvalue:2000 rangesize:100000 maxranges:99\n';;
 *) exit 8;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/ctdb"), []byte(script), 0700))
	launch := smbLaunch{Clustered: true, CTDBPNN: new(uint64), AD: &smbADMember{Workgroup: "EXAMPLE"}}
	raw, _ := json.Marshal(launch)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), raw))
	return root, snap
}

func TestSMBADStateUsesPinnedKeysAndRefusesMissingRecords(t *testing.T) {
	root, snap := smbADProcessFixture(t)
	launch, err := loadSMBLaunch(root)
	require.NoError(t, err)
	var raw []byte

	require.NoError(t, checkSMBADMember(context.Background(), root, false))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "missing-idmap"), nil, 0600))
	require.Error(t, checkSMBADMember(context.Background(), root, false), "no idmap reconstruction on restart")
	raw, _ = json.Marshal(launch.AD)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "ad-initializing"), raw))
	require.NoError(t, checkSMBADMember(context.Background(), root, true), "only initial actor may create first idmap")
	require.Error(t, checkSMBADMember(context.Background(), root, false), "serving never accepts initialization permission as readiness")
}

func TestSMBADSocketMountpointPreparesOnlyEmptyParent(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "samba/winbindd")
	require.NoError(t, prepareSMBWinbindMountpoint(target, uint32(os.Geteuid())))
	require.NoError(t, os.WriteFile(filepath.Join(base, "samba/host-state"), []byte("preserve"), 0600))
	require.Error(t, prepareSMBWinbindMountpoint(target, uint32(os.Geteuid())))
	require.FileExists(t, filepath.Join(base, "samba/host-state"))
}

func TestSMBADJoinReferenceRefreshIsNotServingConfig(t *testing.T) {
	c := nativeADCandidate()
	c.Logical = json.RawMessage(`{"auth_mode":"active-directory","domain_settings":{"realm":"EXAMPLE.TEST","join_sources":[{"source_type":"resource","ref":"old"}]}}`)
	before := c.configurationFingerprint()
	c.Logical = json.RawMessage(strings.Replace(string(c.Logical), `"old"`, `"replacement"`, 1))
	require.Equal(t, before, c.configurationFingerprint(), "logical join references are bootstrap inputs too")
}

func TestSMBADJoinFailureRetainsAttemptAcrossEnableRetry(t *testing.T) {
	python := os.Getenv("MICROCEPH_SMB_TEST_PYTHON")
	if python == "" {
		t.Skip("requires pinned real bindings")
	}
	c := nativeADCandidate()
	c.Spec.ConfigURI = "rados://.smb/alpha/config.smb"
	c.Config = json.RawMessage(`{"samba-container-config":"v0","configs":{"alpha":{"instance_name":"alpha","instance_features":["ctdb"],"globals":["domain"],"shares":[]}},"shares":{},"globals":{"domain":{"options":{"security":"ads","realm":"EXAMPLE.TEST","workgroup":"EXAMPLE","idmap config * : backend":"autorid","idmap config * : range":"2000-9999999"}}}}`)
	runSMBStartupFixture(t, "ad-join", true, python, &c, func(state interfaces.StateInterface, r *smbStartupRunner) {
		require.NotContains(t, r.events, "start smbd")
		r.events = nil
		require.ErrorContains(t, enableSMBLocked(context.Background(), state, "alpha", true), "uncertain")
		require.NotContains(t, r.events, "ad join")
		require.NotContains(t, r.events, "start smbd")
		var retained database.SMBADState
		require.NoError(t, state.ClusterState().Database().Transaction(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
			var err error
			retained, err = database.GetSMBADState(ctx, tx, "alpha", "a")
			return err
		}))
		require.Equal(t, "1", retained.JoinOwner)
		require.False(t, retained.Joined)
	})
}

func TestSMBADReadinessRefusesLossBeforeAllocatingMappings(t *testing.T) {
	root, snap := smbADProcessFixture(t)
	script := `#!/bin/sh
echo "$@" >> "$SNAP/wbinfo-calls"
case "$1" in
 --domain-info=EXAMPLE) echo 'SID               : S-1-5-21-1-2-3';;
 --sid-to-gid=*) echo 20000;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/wbinfo"), []byte(script), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/getent"), []byte("#!/bin/sh\n[ \"$1\" = group ] && [ \"$2\" = 20000 ] && echo 'EXAMPLE\\users:x:20000:'\n"), 0700))
	require.NoError(t, checkSMBADReady(context.Background(), root))
	calls, err := os.ReadFile(filepath.Join(snap, "wbinfo-calls"))
	require.NoError(t, err)
	require.Contains(t, string(calls), "--check-secret")
	require.Contains(t, string(calls), "--sid-to-gid=S-1-5-21-1-2-3-513")
	require.NoError(t, os.Remove(filepath.Join(snap, "wbinfo-calls")))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "missing-idmap"), nil, 0600))
	require.Error(t, checkSMBADReady(context.Background(), root))
	_, err = os.Stat(filepath.Join(snap, "wbinfo-calls"))
	require.True(t, os.IsNotExist(err), "missing initialized mapping state must refuse before lookup can allocate")
}

func TestSMBADSocketIdleShutdownIsBounded(t *testing.T) {
	base, err := os.MkdirTemp("", "smb-ad-idle-")
	require.NoError(t, err)
	defer os.RemoveAll(base)
	listener, guard, err := openSMBPrivateListener(filepath.Join(base, "ad.socket"))
	require.NoError(t, err)
	defer guard.Close()
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveSMBADControls(ctx, listener, func(context.Context, byte) error { return nil }) }()
	conn, err := net.Dial("unix", listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		_ = conn.Close()
		<-done
		t.Fatal("idle peer delayed app shutdown")
	}
}

func TestSMBADAppSupervisesWinbindChildAndReadinessSocket(t *testing.T) {
	root, snap := nativeCTDBFixture(t)
	for _, dir := range []string{"samba/run", "diagnostics"} {
		require.NoError(t, smbPrivateDir(filepath.Join(root, dir)))
	}
	launch := smbLaunch{Clustered: true, CTDBPNN: new(uint64), AD: &smbADMember{Realm: "EXAMPLE.TEST", Workgroup: "EXAMPLE", NetBIOSName: "ALPHA"}}
	*launch.CTDBPNN = 2
	raw, _ := json.Marshal(launch)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), raw))
	raw, err := os.ReadFile(filepath.Join(snap, "bin/ctdb"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/ctdb"), append([]byte("#!/bin/sh\nif [ \"$1\" = -s ]; then echo retained; exit 0; fi\n"), raw...), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/wbinfo"), []byte("#!/bin/sh\ncase \"$1\" in --domain-info=*) echo 'SID : S-1-5-21-1-2-3';; --sid-to-gid=*) echo 20000;; esac\n"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/getent"), []byte("#!/bin/sh\necho 'EXAMPLE\\users:x:20000:'\n"), 0700))
	child := filepath.Join(snap, "bin/child")
	require.NoError(t, os.WriteFile(child, []byte("#!/bin/sh\necho $$ > \"$SNAP/child.pid\"\nexec sleep 60\n"), 0700))
	raw, _ = json.Marshal(launch.AD)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "ad-initializing"), raw))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveSMBADRuntime(ctx, root, smbRolePlan{Args: []string{child}}) }()
	require.Eventually(t, func() bool { return smbADControl(ctx, root, "ready") == nil }, 3*time.Second, 20*time.Millisecond)
	_, err = os.Stat(filepath.Join(root, "ad-initializing"))
	require.True(t, os.IsNotExist(err), "first usable readiness must revoke permission to initialize mappings")
	raw, err = os.ReadFile(filepath.Join(snap, "child.pid"))
	require.NoError(t, err)
	pid := strings.TrimSpace(string(raw))
	cancel()
	require.NoError(t, <-done)
	_, err = os.Stat("/proc/" + pid)
	require.True(t, os.IsNotExist(err), "app shutdown must reap Winbind")
	// An exited child must fail the app so snapd can restart it; a PID is not health.
	require.NoError(t, os.WriteFile(child, []byte("#!/bin/sh\nexit 1\n"), 0700))
	err = serveSMBADRuntime(context.Background(), root, smbRolePlan{Args: []string{child}})
	require.ErrorContains(t, err, "Winbind child exited")
}

func TestSMBADServingRequiresWinbindInAdditionToRetainedState(t *testing.T) {
	root, _ := smbADProcessFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ops := nativeSMBRuntimeOps(ctx, root, "smbd")
	require.Error(t, ops.member(ctx, root, false), "retained TDB records are not enough to launch smbd without usable Winbind")
}

func TestSMBADJoinProcessUsesPinnedBindingAndPrivateStdin(t *testing.T) {
	python := os.Getenv("MICROCEPH_SMB_TEST_PYTHON")
	if python == "" {
		t.Skip("requires pinned bindings")
	}
	useSMBNativeProcesses(t)
	root, snap := smbADProcessFixture(t)
	c := nativeADCandidate()
	plan, err := projectSMBNative(c, root, nativeTestBinding(t))
	require.NoError(t, err)
	for _, name := range []string{"config/candidate.json", "config/context.json", "config/smb.conf"} {
		require.NoError(t, writeSMBNativeFile(filepath.Join(root, name), plan.Files[name]))
	}
	require.NoError(t, os.Symlink(python, filepath.Join(snap, "bin/python3")))
	native, err := os.ReadFile("../../snapcraft/smb/native.py")
	require.NoError(t, err)
	require.NoError(t, writeSMBNativeFile(filepath.Join(snap, "smb/native.py"), native))
	script := `#!/bin/sh
if [ "$5" = getdbmap ]; then
 if [ -e "$SNAP/joined" ]; then echo 'dbid:0x1 name:secrets.tdb path:retained PERSISTENT'; else echo 'Number of databases:0'; fi
elif [ "$5" = pfetch ] && [ -e "$SNAP/joined" ]; then echo retained
else exit 9
fi
`
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/ctdb"), []byte(script), 0700))
	netScript := `#!/bin/sh
umask 077
printf '%s\n' "$@" > "$SNAP/net-argv"
IFS= read -r password
printf '%s' "$password" > "$SNAP/net-input"
printf x >> "$SNAP/attempts"
touch "$SNAP/joined"
`
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/net"), []byte(netScript), 0700))
	require.NoError(t, runSMBADJoin(context.Background(), root))
	raw, err := os.ReadFile(filepath.Join(snap, "net-argv"))
	require.NoError(t, err)
	require.Contains(t, string(raw), filepath.Join(root, "config/smb.conf"))
	require.Contains(t, string(raw), "--no-dns-updates")
	require.Contains(t, string(raw), "joiner-a")
	require.NotContains(t, string(raw), "first-private")
	require.NotContains(t, string(raw), "joiner-b")
	raw, err = os.ReadFile(filepath.Join(snap, "net-input"))
	require.NoError(t, err)
	require.Equal(t, "first-private", string(raw))
	require.FileExists(t, filepath.Join(root, "ad-initializing"))
	require.ErrorContains(t, runSMBADJoin(context.Background(), root), "retained AD databases")
	raw, err = os.ReadFile(filepath.Join(snap, "attempts"))
	require.NoError(t, err)
	require.Equal(t, "x", string(raw))
}

func TestSMBADJoinPreservesFirstPrivateFailure(t *testing.T) {
	root, snap := smbADProcessFixture(t)
	path := filepath.Join(snap, "bin/ctdb")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho first-private-failure >&2\nexit 1\n"), 0700))
	err := runSMBADJoin(context.Background(), root)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "first-private-failure")
	first, err := os.ReadFile(filepath.Join(root, "diagnostics/ad-initial-join-error"))
	require.NoError(t, err)
	require.Contains(t, string(first), "first-private-failure")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho later >&2\nexit 1\n"), 0700))
	require.Error(t, runSMBADJoin(context.Background(), root))
	retained, err := os.ReadFile(filepath.Join(root, "diagnostics/ad-initial-join-error"))
	require.NoError(t, err)
	require.Equal(t, first, retained)
}
