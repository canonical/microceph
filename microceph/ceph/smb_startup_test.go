package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

type smbStartupRunner struct {
	t                *testing.T
	sourceCandidate  *smbCandidate
	db               *sql.DB
	events           []string
	active           map[string]bool
	readyRoles       map[string]bool
	startupErr       error
	failure          string
	cancel           context.CancelFunc
	root             string
	python           string
	nonclustered     bool
	refresh          bool
	changed          bool
	unready          bool
	sourceFailure    bool
	logical          string
	spec             string
	containment      bool
	reads            int
	disabled         bool
	torn             bool
	expectedIdentity string
	expectedPNN      uint64
	onStop           func()
	onReady          func(context.Context, string) error
}

func (r *smbStartupRunner) RunCommand(name string, args ...string) (string, error) {
	return r.RunCommandContext(context.Background(), name, args...)
}

func (r *smbStartupRunner) RunCommandContext(ctx context.Context, name string, args ...string) (string, error) {
	if r.refresh && name == "ceph" && args[0] != "smb" && args[0] != "--id" {
		r.t.Fatalf("forbidden provisioning during refresh: %s %q", name, args)
	}
	if name == "ceph" {
		switch args[0] {
		case "mgr":
			return `{"enabled_modules":["smb"]}`, nil
		case "config":
			return "false", nil
		case "smb":
			if r.sourceCandidate != nil {
				return `{"resource_type":"ceph.smb.cluster","cluster_id":"alpha",` + strings.TrimPrefix(string(r.sourceCandidate.Logical), "{"), nil
			}
			if r.refresh {
				r.reads++
				if r.logical != "" {
					return r.logical, nil
				}
			}
			return `{"resource_type":"ceph.smb.cluster","cluster_id":"alpha"}`, nil
		case "auth":
			if args[2] != "client.smb.config.cluster.alpha" {
				var raw string
				require.NoError(r.t, r.db.QueryRow("SELECT config FROM service_groups WHERE group_id='alpha'").Scan(&raw))
				identity := r.expectedIdentity
				if identity == "" {
					identity = "1"
				}
				require.Contains(r.t, raw, `"`+identity+`":"a"`, "reserve native owner before runtime identities")
				if args[1] == "caps" {
					require.Equal(r.t, []string{"auth", "caps", "client.smb.cluster.alpha", "mon", "allow r", "osd", "allow rwx pool=.smb namespace=alpha"}, args)
					r.events = append(r.events, "caps "+args[2])
					return "", nil
				}
				if args[2] == "client.smb.cluster.alpha" {
					require.Equal(r.t, []string{"auth", "get-or-create", "client.smb.cluster.alpha", "--format", "json"}, args)
				}
				r.events = append(r.events, "key "+args[2])
			}
			return `[{"entity":"` + args[2] + `","key":"AQAAAA=="}]`, nil
		}
	}
	if name == "ceph" && len(args) > 2 && args[0] == "--id" && r.sourceCandidate != nil && args[len(args)-2] == "get" {
		index := 0
		if strings.HasSuffix(args[len(args)-1], "join-b") {
			index = 1
		}
		return string(r.sourceCandidate.Joins[index]), nil
	}
	if name == "rados" {
		if r.refresh {
			r.reads++
		}
		if r.sourceFailure {
			return "", errors.New("transport unavailable")
		}
		object := args[len(args)-2]
		var data string
		switch object {
		case "spec.smb":
			data = `{"service_type":"smb","service_id":"alpha","spec":{"cluster_id":"alpha","features":["clustered"],"user_sources":["rados://.smb/alpha/users.smb"],"config_uri":"rados://.smb/alpha/config.smb","cluster_meta_uri":"rados://.smb/alpha/cluster.meta","cluster_lock_uri":"rados://.smb/alpha/cluster.lock"}}`
		case "users.smb":
			data = `{"users":{"all_entries":[{"name":"alice","password":"changed","uid":2000,"gid":2000}]},"groups":{"all_entries":[{"name":"team","gid":2000}]}}`
		case "config.smb":
			data = `{"samba-container-config":"v0","globals":{},"shares":{},"configs":{"alpha":{"instance_name":"alpha","instance_features":["ctdb"],"shares":[],"globals":[]}}}`
		default:
			return "", errors.New("Error ENOENT")
		}
		if object == "spec.smb" && r.python != "" {
			endpoint := `"custom_ports":{"smb":1445,"smbmetrics":19922},`
			if r.changed {
				endpoint = `"bind_addrs":[{"address":"2001:db8::7"}],"custom_ports":{"smb":2445,"smbmetrics":29922},`
			}
			data = strings.Replace(data, `"spec":{`, `"spec":{`+endpoint, 1)
		}
		if object == "spec.smb" && r.spec != "" {
			data = r.spec
		}
		if !r.changed && object == "users.smb" {
			data = strings.Replace(data, `"password":"changed"`, `"password":"initial"`, 1)
			data = strings.Replace(data, `"groups":{"all_entries":[{"name":"team","gid":2000}]}`, `"groups":{"all_entries":[]}`, 1)
		}
		if r.changed && object == "config.smb" {
			data = strings.Replace(data, `"shares":{}`, `"shares":{"data":{"options":{"path":"/srv/data"}}}`, 1)
			data = strings.Replace(data, `"shares":[]`, `"shares":["data"]`, 1)
		}
		policyOptions := map[string]string{
			"policy-auto-pdc":            `"security":"user","domain logons":"yes","domain master":"yes"`,
			"policy-auto-bdc":            `"security":"auto","server role":"auto","domain logons":"yes","domain master":"no"`,
			"policy-override":            `"realm":"EXAMPLE.TEST"`,
			"policy-role-spelling":       `"serverrole":"active directory domain controller"`,
			"policy-security-spelling":   `"sec urity":"ads"`,
			"policy-realm-spelling":      `"r e a l m":"EXAMPLE.TEST"`,
			"policy-clustering-spelling": `"clu\tstering":"yes"`,
		}
		if object == "config.smb" && policyOptions[r.failure] != "" {
			data = strings.Replace(data, `"globals":{}`, `"globals":{"policy":{"options":{`+policyOptions[r.failure]+`}}}`, 1)
			data = strings.Replace(data, `"globals":[]`, `"globals":["policy"]`, 1)
		}
		if r.nonclustered {
			data = strings.ReplaceAll(data, `["clustered"]`, `[]`)
			data = strings.ReplaceAll(data, `["ctdb"]`, `[]`)
		}
		if r.sourceCandidate != nil {
			c := r.sourceCandidate
			switch object {
			case "spec.smb":
				raw, _ := json.Marshal(map[string]any{"service_type": "smb", "service_id": "alpha", "spec": c.Spec})
				data = string(raw)
			case "config.smb":
				data = string(c.Config)
			}
		}
		if args[len(args)-1] == "-" {
			return data, nil
		}
		return "", os.WriteFile(args[len(args)-1], []byte(data), 0600)
	}
	if name == "/test-snap/bin/testparm" {
		require.Equal(r.t, "--suppress-prompt", args[0])
		require.Equal(r.t, filepath.Join(r.root, "config/next/config/smb.conf"), args[1])
		return "", nil // Only the external validation command is intercepted.
	}
	if name == "/test-snap/bin/python3" {
		return r.runUpstreamBinding(ctx, args[2], args[3])
	}
	if name == "snapctl" {
		role := strings.TrimPrefix(args[1], "microceph.")
		if r.refresh && args[0] == "services" && (role == "mon" || role == "osd") {
			return "active", nil // unrelated startup checks, not service operations
		}
		if r.refresh && !r.containment {
			require.NotContains(r.t, []string{"start", "stop"}, args[0], "ordinary refresh never enters app enable/disable/start/stop")
			require.NotContains(r.t, args, "--enable")
			require.NotContains(r.t, args, "--disable")
			require.NotContains(r.t, []string{"ctdbd", "ctdb-nodes"}, role, "refresh must not control CTDB")
		}
		switch args[0] {
		case "restart":
			r.events = append(r.events, "restart "+role)
			if r.failure == "restart" {
				return "", errors.New("restart failed")
			}
			return "", nil
		case "start":
			if role == "ctdbd" {
				conf, err := os.ReadFile(filepath.Join(constants.GetPathConst().ConfPath, "ceph.conf"))
				require.NoError(r.t, err)
				require.Contains(r.t, string(conf), "[client.smb.cluster.alpha]\nkeyring = "+filepath.Join(r.root, "identity/recovery.keyring")+"\n", "canonical client section must precede the first CTDB start")
			}
			r.events = append(r.events, "start "+role)
			if role == "smbd" && !r.nonclustered {
				require.True(r.t, r.readyRoles["ctdbd"], "CTDB ready before SAM/Samba")
			}
			if (r.failure == "start" && role == "ctdbd") || (r.failure == "monitor-start" && role == "ctdb-nodes") || (r.failure == "samba-start" && role == "smbd") {
				r.active[role] = true
				return "", errors.New("uncertain start")
			}
			r.active[role] = true
			return "", nil
		case "stop":
			if r.failure == "stop" {
				return "", errors.New("uncertain stop")
			}
			if r.onStop != nil {
				callback := r.onStop
				r.onStop = nil
				callback()
			}
			r.events = append(r.events, "stop "+role)
			r.active[role] = false
			return "", nil
		case "services":
			if r.refresh {
				if r.containment {
					return args[1] + " disabled inactive", nil
				}
				if r.disabled {
					return args[1] + " disabled inactive", nil
				}
				return args[1] + " enabled active", nil
			}
			return args[1] + " disabled inactive", nil
		}
	}
	r.t.Fatalf("unexpected command %s %q", name, args)
	return "", errors.New("unexpected command")
}

func (r *smbStartupRunner) compile(ctx context.Context, root string, candidate smbCandidate) error {
	r.root = root

	if r.nonclustered {
		require.Nil(r.t, candidate.CTDB)
	} else {
		require.NotNil(r.t, candidate.CTDB)
		require.Equal(r.t, r.expectedPNN, candidate.CTDB.Member.PNN)
		require.Equal(r.t, "192.0.2.1", candidate.CTDB.Member.Node)
		identity := r.expectedIdentity
		if identity == "" {
			identity = "1"
		}
		require.Equal(r.t, identity, candidate.CTDB.Member.Identity)
		key, err := os.ReadFile(filepath.Join(root, "identity/recovery.keyring"))
		require.NoError(r.t, err)
		require.Contains(r.t, string(key), "[client.smb.cluster.alpha]")
	}
	r.events = append(r.events, "compile")
	if r.refresh && r.torn {
		r.changed = false
	}
	if r.failure == "compile" {
		return errors.New("compile failed")
	}
	if r.python != "" {
		return compileSMBNative(ctx, root, candidate)
	}
	require.NoError(r.t, os.MkdirAll(filepath.Join(root, "config/next/config"), 0700))
	launch, err := json.Marshal(smbLaunch{Clustered: !r.nonclustered,
		FrontendAddress: candidate.Endpoint.FrontendAddress, SMBPort: candidate.Endpoint.SMBPort, MetricsPort: candidate.Endpoint.MetricsPort})
	require.NoError(r.t, err)
	return os.WriteFile(filepath.Join(root, "config/next/config/context.json"), launch, 0600)
}

func (r *smbStartupRunner) prepareCTDB(ctx context.Context, root, snap string) error {
	require.False(r.t, r.refresh, "refresh never prepares CTDB")

	r.events = append(r.events, "prepare-ctdb")
	if r.failure == "prepare" {
		return errors.New("event preparation failed")
	}
	return nil
}

func (r *smbStartupRunner) publish(ctx context.Context, root string, configOnly bool) error {
	require.True(r.t, !r.refresh || configOnly, "refresh never uses full publication")

	r.events = append(r.events, "publish")
	if r.failure == "publish" {
		return errors.New("publish failed")
	}
	if r.python != "" {
		return r.publishFiles(ctx, root, configOnly)
	}
	return nil
}

func (r *smbStartupRunner) ready(ctx context.Context, root, role string) error {
	if !r.active[role] || (r.unready && role == "smbd") {
		return errors.New("not started")
	}
	r.events = append(r.events, "ready "+role)
	if r.onReady != nil {
		err := r.onReady(ctx, role)
		if err != nil {
			return err
		}
	}
	if (r.failure == "readiness" && role == "ctdbd") || (r.failure == "monitor-readiness" && role == "ctdb-nodes") {
		r.cancel()
		return context.Canceled
	}
	r.readyRoles[role] = true
	return nil
}

func TestSMBClusteredNativeStartupSQLOrderAndCompensation(t *testing.T) {
	for _, failure := range []string{"", "recovery-config", "compile", "publish", "prepare", "start", "monitor-start", "samba-start", "readiness", "monitor-readiness", "record", "applied"} {
		t.Run(failure, func(t *testing.T) {
			runSMBStartup(t, failure, true, "")
		})
	}
}

// publishFiles uses the shared publisher; failures exercise real atomic writes.
func (r *smbStartupRunner) publishFiles(ctx context.Context, root string, configOnly bool) error {
	if r.failure == "publish-partial" {
		// Exercise a real failed atomic rename after candidate.json publication.
		path := filepath.Join(root, "config/passwd")
		backup := path + ".test-retained"
		require.NoError(r.t, os.Rename(path, backup))
		require.NoError(r.t, os.Mkdir(path, 0700))
		defer func() {
			require.NoError(r.t, os.Remove(path))
			require.NoError(r.t, os.Rename(backup, path))
		}()
	}
	return publishSMBNative(ctx, root, configOnly)
}

func (r *smbStartupRunner) runUpstreamBinding(ctx context.Context, command, request string) (string, error) {
	if r.failure == "binding" {
		return "", errors.New("source-password-private")
	}

	native, err := filepath.Abs("../../snapcraft/smb/native.py")
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, r.python, "-B", native, command, request)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	out, err := cmd.Output()
	return string(out), err
}

func TestSMBNativeStartupRealCompilerContext(t *testing.T) {
	python := os.Getenv("MICROCEPH_SMB_TEST_PYTHON")
	if python == "" {
		t.Skip("set MICROCEPH_SMB_TEST_PYTHON and PYTHONPATH to exercise the real native compiler with Sambacc")
	}
	for _, clustered := range []bool{true, false} {
		name := "nonclustered"
		if clustered {
			name = "clustered"
		}
		t.Run(name, func(t *testing.T) {
			runSMBStartup(t, "", clustered, python)
		})
	}
}

func runSMBStartup(t *testing.T, failure string, clustered bool, python string, after ...func(interfaces.StateInterface, *smbStartupRunner)) {
	t.Helper()
	runSMBStartupFixture(t, failure, clustered, python, nil, after...)
}

func runSMBStartupFixture(t *testing.T, failure string, clustered bool, python string, source *smbCandidate, after ...func(interfaces.StateInterface, *smbStartupRunner)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "startup.db")+"?_foreign_keys=on")
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE core_cluster_members(id INTEGER PRIMARY KEY, name TEXT UNIQUE, address TEXT NOT NULL); INSERT INTO core_cluster_members VALUES(1,'a','192.0.2.1:7443');`)
	require.NoError(t, err)
	tx, err := db.Begin()
	require.NoError(t, err)
	require.NoError(t, database.SchemaExtensions[5](ctx, tx))
	require.NoError(t, tx.Commit())
	runner := &smbStartupRunner{sourceCandidate: source, t: t, db: db, active: map[string]bool{}, readyRoles: map[string]bool{}, failure: failure, cancel: cancel, python: python, nonclustered: !clustered}
	oldRunner, oldNetwork, oldFetch := common.ProcessExec, common.Network, fetchConfigDb
	oldInspect := inspectSMBPolicyFunc
	inspectSMBPolicyFunc = func(ctx context.Context, root string, candidate smbCandidate) (*smbADMember, error) {
		if python != "" {
			return inspectSMBPolicy(ctx, root, candidate)
		}
		return nil, nil
	}
	defer func() { inspectSMBPolicyFunc = oldInspect }()
	oldCompile, oldPublish, oldPrepare, oldReady := compileSMBNativeFunc, publishSMBNativeFunc, prepareSMBCTDBFilesFunc, smbReadyNativeFunc
	compileSMBNativeFunc, publishSMBNativeFunc, prepareSMBCTDBFilesFunc, smbReadyNativeFunc = runner.compile, runner.publish, runner.prepareCTDB, runner.ready
	defer func() {
		compileSMBNativeFunc, publishSMBNativeFunc, prepareSMBCTDBFilesFunc, smbReadyNativeFunc = oldCompile, oldPublish, oldPrepare, oldReady
	}()
	common.ProcessExec = runner
	common.Network = smbEndpointNetwork{addresses: []string{"198.51.100.7", "2001:db8::7"}}
	fetchConfigDb = func(context.Context, interfaces.StateInterface) (map[string]string, error) {
		return map[string]string{"public_network": "198.51.100.0/24"}, nil
	}
	defer func() { common.ProcessExec, common.Network, fetchConfigDb = oldRunner, oldNetwork, oldFetch }()
	root := t.TempDir()
	t.Setenv("SNAP_COMMON", root)
	t.Setenv("SNAP", "/test-snap")
	oldPaths, oldUpdate := constants.GetPathConst, updateConfigFunc
	constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: root} }
	// Exercise the real SQL reservation projection and canonical template. The
	// unrelated global/admin config queries have their own UpdateConfig tests.
	updateConfigFunc = func(ctx context.Context, state interfaces.StateInterface) error {
		clients, err := getSMBRecoveryClients(ctx, state)
		if err != nil {
			return err
		}
		if failure == "recovery-config" {
			path := filepath.Join(root, constants.CephConfFileName)
			require.NoError(t, os.Rename(path, path+".retained"))
			require.NoError(t, os.Mkdir(path, 0700))
			defer func() {
				require.NoError(t, os.Remove(path))
				require.NoError(t, os.Rename(path+".retained", path))
			}()
		}
		return NewCephConfig(constants.CephConfFileName).WriteConfig(map[string]any{
			"fsid": "fixture-fsid", "monitors": "192.0.2.1", "pubNet": "198.51.100.0/24",
			"smbRecoveryClients": clients,
		}, 0644)
	}
	defer func() { constants.GetPathConst, updateConfigFunc = oldPaths, oldUpdate }()
	require.NoError(t, os.WriteFile(filepath.Join(root, "ceph.conf"), []byte("[global]\n"), 0600))
	mockDB := &mocks.MockDB{TxFn: func(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer func() {
			// Best-effort cleanup; a successful commit already closes the transaction.
			_ = tx.Rollback()
		}()
		err = fn(ctx, tx)
		if err != nil {
			return err
		}
		if runner.refresh {
			return tx.Commit()
		}
		var count int
		require.NoError(t, tx.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
		if count > 0 {
			if clustered {
				require.True(t, runner.readyRoles["ctdbd"] && runner.readyRoles["ctdb-nodes"])
			}
			require.True(t, runner.readyRoles["smbd"] && runner.readyRoles["smbmetrics"])
			runner.events = append(runner.events, "record")
			if failure == "record" {
				return errors.New("record failed")
			}
			if failure == "applied" {
				require.NoError(t, os.Mkdir(filepath.Join(runner.root, "applied.json"), 0700))
			}
		}
		return tx.Commit()
	}}
	oldADControl := smbADControlFunc
	if source != nil {
		smbADControlFunc = func(ctx context.Context, root, op string) error {
			var state database.SMBADState
			tx, e := db.Begin()
			require.NoError(t, e)
			state, e = database.GetSMBADState(ctx, tx, "alpha", "a")
			require.NoError(t, e)
			require.NoError(t, tx.Rollback())
			if op == "join" {
				require.NotEmpty(t, state.JoinOwner)
				require.False(t, state.Joined)
				require.True(t, runner.readyRoles["ctdbd"])
				require.False(t, runner.active["smbd"])
			}
			if op == "start" {
				require.True(t, state.Joined)
			}
			if op != "probe" {
				runner.events = append(runner.events, "ad "+op)
				if op == "join" && failure == "ad-join" {
					return errors.New("ambiguous external result")
				}
			}
			return nil
		}
	}
	defer func() { smbADControlFunc = oldADControl }()
	state := interfaces.CephState{State: &mocks.MockState{ClusterName: "a", DBObj: mockDB}}
	err = enableSMBLocked(ctx, state, "alpha", true)
	runner.startupErr = err
	if failure == "" {
		require.NoError(t, err)
		require.Contains(t, runner.events, "record")
		got := []string{}
		for _, event := range runner.events {
			if !strings.HasPrefix(event, "stop ") {
				got = append(got, event)
			}
		}
		want := []string{"key client.smb.fs.cluster.alpha", "compile", "publish", "start smbd", "ready smbd", "start smbmetrics", "ready smbmetrics", "record"}
		if clustered {
			want = []string{"key client.smb.fs.cluster.alpha", "key client.smb.cluster.alpha", "caps client.smb.cluster.alpha", "compile", "publish", "prepare-ctdb", "start ctdbd", "start ctdb-nodes", "ready ctdbd", "ready ctdb-nodes", "start smbd", "ready smbd", "start smbmetrics", "ready smbmetrics", "record"}
		}
		if source != nil {
			for i, event := range want {
				if event == "start smbd" {
					want = append(want[:i], append([]string{"start winbindd", "ad join", "ad start", "ready winbindd"}, want[i:]...)...)
					break
				}
			}
		}
		require.Equal(t, want, got)
		if python != "" {
			contextBytes, err := os.ReadFile(filepath.Join(runner.root, "config/context.json"))
			require.NoError(t, err)
			var launch map[string]any
			require.NoError(t, json.Unmarshal(contextBytes, &launch))
			require.Equal(t, clustered, launch["clustered"])
			require.Equal(t, "198.51.100.7", launch["frontend_address"])
			require.Equal(t, float64(1445), launch["smb_port"])
			require.Equal(t, float64(19922), launch["metrics_port"])
			var info string
			require.NoError(t, db.QueryRow("SELECT info FROM grouped_services").Scan(&info))
			require.JSONEq(t, fmt.Sprintf(`{"config_uri":"rados://.smb/alpha/config.smb","clustered":%t,"frontend_address":"198.51.100.7","smb_port":1445,"metrics_address":"0.0.0.0","metrics_port":19922}`, clustered), info)
			_, err = os.Stat(filepath.Join(runner.root, "config/next/context.json"))
			require.ErrorIs(t, err, os.ErrNotExist, "no obsolete flat context can mask the seam")
		}
	} else {
		require.Error(t, err)
		for _, active := range runner.active {
			require.False(t, active, "compensation stops attempted apps")
		}
	}
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
	if failure == "" {
		require.Equal(t, 1, count)
	} else {
		require.Zero(t, count)
	}
	var raw string
	if failure == "binding" || strings.HasPrefix(failure, "policy-") {
		require.ErrorIs(t, db.QueryRow("SELECT config FROM service_groups").Scan(&raw), sql.ErrNoRows, "policy inspection fails before reservation")
	} else {
		require.NoError(t, db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
		require.Contains(t, raw, `"1":"a"`, "failure retains ownership and rank")
	}
	for _, callback := range after {
		callback(state, runner)
	}
}
