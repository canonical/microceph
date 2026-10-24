package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

func TestSMBADRolesKeepNativeDependencyOrderAndRejectSAM(t *testing.T) {
	c := nativeADCandidate()
	roles, err := smbCoreRoles(c.Spec, true)
	require.NoError(t, err)
	require.Equal(t, []string{"ctdbd", "cephfs-proxy", "winbindd", "smbd", "smbmetrics"}, roles)
	require.NoError(t, validateSMBLogicalPolicy(c.Logical, c.Spec))
	c.Spec.Features = []string{"domain"}
	require.Error(t, validateSMBLogicalPolicy(c.Logical, c.Spec))
	root, _ := smbADProcessFixture(t)
	raw, err := json.Marshal(nativeADCandidate())
	require.NoError(t, err)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/candidate.json"), raw))
	require.ErrorContains(t, prepareSMBAccounts(context.Background(), root), "must not import local SAM")
}

func TestSMBADBoundedProcessKillsChildAndProtectsSecrets(t *testing.T) {
	useSMBNativeProcesses(t)
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
	useSMBNativeProcesses(t)
	base, err := os.MkdirTemp("", "smb-ad-process-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	root := filepath.Join(base, "alpha")
	snap := t.TempDir()
	t.Setenv("SNAP", snap)
	require.NoError(t, os.MkdirAll(filepath.Join(snap, "bin"), 0700))
	// Samba secrets uses string_tdb_data (no NUL); autorid CONFIG uses
	// string_term_tdb_data. CTDB accepts binary keys as 0x-prefixed hex.
	// Like real pfetch, an absent key returns success with empty output.
	script := `#!/bin/sh
[ "$1" = -t ] && [ "$2" = 5 ] || exit 6
printf '%s\n' "$*" >> "$SNAP/ctdb-calls"
if [ "$3" = getdbmap ]; then
 echo 'dbid:0x1 name:secrets.tdb path:retained PERSISTENT'
 if [ ! -e "$SNAP/missing-database" ]; then echo 'dbid:0x2 name:autorid.tdb path:retained PERSISTENT'; fi
 exit 0
fi
[ "$3" = pfetch ] || exit 7
case "$4:$5" in
 secrets.tdb:SECRETS/MACHINE_PASSWORD/EXAMPLE|secrets.tdb:SECRETS/SID/EXAMPLE) printf 'retained\n';;
 autorid.tdb:0x434f4e46494700) if [ ! -e "$SNAP/missing-idmap" ]; then printf 'minvalue:2000 rangesize:100000 maxranges:99\n'; fi;;
 *) exit 0;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/ctdb"), []byte(script), 0700))
	launch := smbLaunch{Clustered: true, CTDBPNN: new(uint64), AD: &smbADMember{Workgroup: "EXAMPLE"}}
	raw, _ := json.Marshal(launch)
	require.NoError(t, smbAtomicWrite(filepath.Join(filepath.Dir(root), "owner.json"), []byte("alpha")))
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), raw))
	return root, snap
}

func TestSMBADCTDBUsesPinnedCLIInPrivateNamespace(t *testing.T) {
	root, snap := smbADProcessFixture(t)
	// CTDB 4.23.6 has no production socket override. Only this fixed client
	// gets the compatibility bind inside a daemon-created child namespace.
	cmd, err := newSMBPrivateCommand(context.Background(), root, []string{filepath.Join(snap, "bin/ctdb"), "getdbmap"})
	require.NoError(t, err)
	require.Contains(t, cmd.Args[2], "smb_setup_ctdb")
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/ctdb"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700))
	for _, args := range [][]string{
		{"getdbmap"},
		{"pfetch", "secrets.tdb", "SECRETS/SID/EXAMPLE"},
		{"pfetch", "autorid.tdb", "0x434f4e46494700"},
	} {
		t.Run(args[0]+"/"+strings.Join(args[1:], "/"), func(t *testing.T) {
			raw, err := smbADCTDB(context.Background(), root, args...)
			require.NoError(t, err)
			require.Equal(t, append([]string{"-t", "5"}, args...), strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n"))
		})
	}
}

func TestSMBADRecordUsesCTDBHexForNULTerminatedKeys(t *testing.T) {
	root, snap := smbADProcessFixture(t)
	script := "#!/bin/sh\nif [ \"$3\" = getdbmap ]; then echo 'name:secrets.tdb path:retained'; echo 'name:autorid.tdb path:retained'; exit 0; fi\nprintf '%s\\n' \"$@\" > \"$SNAP/record-argv\"\nprintf 'retained\\n'\n"
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/ctdb"), []byte(script), 0700))
	for _, test := range []struct {
		database string
		key      string
		argument string
	}{
		{"secrets.tdb", "SECRETS/MACHINE_PASSWORD/EXAMPLE", "SECRETS/MACHINE_PASSWORD/EXAMPLE"},
		{"secrets.tdb", "SECRETS/SID/EXAMPLE", "SECRETS/SID/EXAMPLE"},
		{"autorid.tdb", "CONFIG", "0x434f4e46494700"},
	} {
		t.Run(test.key, func(t *testing.T) {
			err := smbADRecord(context.Background(), root, test.database, test.key)
			require.NoError(t, err)
			raw, err := os.ReadFile(filepath.Join(snap, "record-argv"))
			require.NoError(t, err)
			require.Equal(t, []string{"-t", "5", "pfetch", test.database, test.argument}, strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n"))
		})
	}
}

func TestSMBADReadinessBudgetsRespectParentDeadline(t *testing.T) {
	for _, probe := range []struct {
		name string
		run  func(context.Context, string) error
	}{
		{"runtime", readySMBADRuntime},
		{"member", checkSMBADReady},
	} {
		t.Run(probe.name, func(t *testing.T) {
			root, snap := smbADProcessFixture(t)
			require.NoError(t, setSMBPrepared(root, "winbindd", true))
			path := filepath.Join(snap, "bin/ctdb")
			script, err := os.ReadFile(path)
			require.NoError(t, err)
			health := "#!/bin/sh\nif [ \"$1\" != -t ]; then\ncase \"$1\" in\npnn) echo 0;;\nnodestatus) exit 0;;\nstatus) printf 'Number of nodes:1\\nRecovery mode:NORMAL (0)\\n';;\n*) exit 1;;\nesac\nexit 0\nfi\n"
			require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(script), "#!/bin/sh\n", health, 1)), 0700))
			old := newSMBPrivateCommandFunc
			t.Cleanup(func() { newSMBPrivateCommandFunc = old })
			var remaining time.Duration
			newSMBPrivateCommandFunc = func(ctx context.Context, root string, args []string) (*exec.Cmd, error) {
				if args[0] != filepath.Join(snap, "bin/wbinfo") {
					return old(ctx, root, args)
				}
				deadline, bounded := ctx.Deadline()
				require.True(t, bounded)
				remaining = time.Until(deadline)
				return nil, context.Canceled
			}
			require.Error(t, probe.run(context.Background(), root))
			require.Greater(t, remaining, 29*time.Second)
			require.LessOrEqual(t, remaining, 30*time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			require.Error(t, probe.run(ctx, root))
			require.Positive(t, remaining)
			require.LessOrEqual(t, remaining, time.Second)
		})
	}
}

func TestSMBADMemberReadsInventoryOncePerObservation(t *testing.T) {
	root, snap := smbADProcessFixture(t)
	err := checkSMBADMember(context.Background(), root, false)
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(snap, "ctdb-calls"))
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(raw), "getdbmap"), "each inventory starts an expensive private namespace")
	require.Equal(t, 3, strings.Count(string(raw), "pfetch"), "both trust records and the retained idmap must still be checked")
}

func TestSMBADMemberNeverFetchesAnAbsentDatabase(t *testing.T) {
	root, snap := smbADProcessFixture(t)
	err := os.WriteFile(filepath.Join(snap, "missing-database"), nil, 0600)
	require.NoError(t, err)
	err = checkSMBADMember(context.Background(), root, false)
	require.ErrorContains(t, err, "database unavailable")
	raw, err := os.ReadFile(filepath.Join(snap, "ctdb-calls"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "pfetch autorid.tdb", "pfetch could attach or create an absent database")
}

func TestSMBADStateUsesPinnedKeysAndRefusesMissingRecords(t *testing.T) {
	root, snap := smbADProcessFixture(t)
	launch, err := loadSMBLaunch(root)
	require.NoError(t, err)
	var raw []byte

	require.NoError(t, checkSMBADMember(context.Background(), root, false))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "missing-idmap"), nil, 0600))
	require.ErrorContains(t, checkSMBADMember(context.Background(), root, false), "AD trust/idmap state lost", "no idmap reconstruction on restart")
	raw, _ = json.Marshal(launch.AD)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "ad-initializing"), raw))
	require.NoError(t, checkSMBADMember(context.Background(), root, true), "only initial actor may create first idmap")
	require.Error(t, checkSMBADMember(context.Background(), root, false), "serving never accepts initialization permission as readiness")
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
	require.NoError(t, setSMBPrepared(root, "winbindd", false))
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

func TestSMBADServingRequiresWinbindInAdditionToRetainedState(t *testing.T) {
	root, _ := smbADProcessFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, setSMBPrepared(root, "winbindd", false))
	require.Error(t, checkSMBADReady(ctx, root), "retained TDB records are not enough without usable Winbind")
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
[ "$1" = -t ] && [ "$2" = 5 ] || exit 6
if [ "$3" = getdbmap ]; then
 if [ -e "$SNAP/joined" ]; then echo 'dbid:0x1 name:secrets.tdb path:retained PERSISTENT'; else echo 'Number of databases:0'; fi
elif [ "$3" = pfetch ] && [ -e "$SNAP/joined" ]; then echo retained
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
