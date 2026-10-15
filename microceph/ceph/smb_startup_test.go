package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	db               *sql.DB
	events           []string
	active           map[string]bool
	readyRoles       map[string]bool
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
	containment      bool
	reads            int
	disabled         bool
	torn             bool
	expectedIdentity string
	expectedPNN      uint64
	onStop           func()
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
				r.events = append(r.events, "key "+args[2])
			}
			return `[{"entity":"` + args[2] + `","key":"AQAAAA=="}]`, nil
		}
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
		if !r.changed && object == "users.smb" {
			data = strings.Replace(data, `"password":"changed"`, `"password":"initial"`, 1)
			data = strings.Replace(data, `"groups":{"all_entries":[{"name":"team","gid":2000}]}`, `"groups":{"all_entries":[]}`, 1)
		}
		if r.changed && object == "config.smb" {
			data = strings.Replace(data, `"shares":{}`, `"shares":{"data":{"options":{"path":"/srv/data"}}}`, 1)
			data = strings.Replace(data, `"shares":[]`, `"shares":["data"]`, 1)
		}
		if r.nonclustered {
			data = strings.ReplaceAll(data, `["clustered"]`, `[]`)
			data = strings.ReplaceAll(data, `["ctdb"]`, `[]`)
		}
		return "", os.WriteFile(args[len(args)-1], []byte(data), 0600)
	}
	if name == "/test-snap/bin/python3" {
		command, root := args[2], args[3]
		r.root = root
		if r.refresh {
			require.NotEqual(r.t, "prepare-ctdb", command, "refresh never prepares CTDB")
			require.NotEqual(r.t, "publish", command, "refresh never uses full startup publication")
		}
		switch command {
		case "compile":
			raw, err := os.ReadFile(filepath.Join(root, "config/candidate.next.json"))
			require.NoError(r.t, err)
			var candidate smbCandidate
			require.NoError(r.t, json.Unmarshal(raw, &candidate))
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
				return "", errors.New("compile failed")
			}
			if r.python != "" {
				return r.runNativeFiles(ctx, "compile", root)
			}
			require.NoError(r.t, os.MkdirAll(filepath.Join(root, "config/next/config"), 0700))
			launch := `{"clustered":true,"ctdb_pnn":0,"smb_port":445}`
			if r.nonclustered {
				launch = `{"clustered":false,"smb_port":445}`
			}
			return "", os.WriteFile(filepath.Join(root, "config/next/config/context.json"), []byte(launch), 0600)
		case "prepare-ctdb":
			r.events = append(r.events, "prepare-ctdb")
			if r.failure == "prepare" {
				return "", errors.New("event preparation failed")
			}
			return "", nil
		case "publish", "publish-config":
			r.events = append(r.events, "publish")
			if r.failure == "publish" {
				return "", errors.New("publish failed")
			}
			if r.python != "" {
				return r.runNativeFiles(ctx, command, root)
			}
			return "", nil
		case "ready":
			role := args[4]
			if !r.active[role] || (r.unready && role == "smbd") {
				return "", errors.New("not started")
			}
			r.events = append(r.events, "ready "+role)
			if (r.failure == "readiness" && role == "ctdbd") || (r.failure == "monitor-readiness" && role == "ctdb-nodes") {
				r.cancel()
				return "", context.Canceled
			}
			r.readyRoles[role] = true
			return "", nil
		}
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

func TestSMBClusteredNativeStartupSQLOrderAndCompensation(t *testing.T) {
	for _, failure := range []string{"", "compile", "publish", "prepare", "start", "monitor-start", "samba-start", "readiness", "monitor-readiness", "record", "applied"} {
		t.Run(failure, func(t *testing.T) {
			runSMBStartup(t, failure, true, "")
		})
	}
}

// runNativeFiles crosses the compiler/publication process edge without replacing
// their filesystem output. Only testparm is intercepted; no daemon is launched.
func (r *smbStartupRunner) runNativeFiles(ctx context.Context, command, root string) (string, error) {
	native, err := filepath.Abs("../../snapcraft/smb/native.py")
	if err != nil {
		return "", err
	}
	const bridge = `
import importlib.util
import os
from pathlib import Path
import sys
from unittest.mock import patch
spec = importlib.util.spec_from_file_location("native_startup_bridge", sys.argv[1])
native = importlib.util.module_from_spec(spec)
spec.loader.exec_module(native)
root = Path(sys.argv[3])
if sys.argv[2] == "compile":
    with patch("subprocess.run") as testparm:
        native.compile_files(root)
    testparm.assert_called_once_with(
        [os.environ["SNAP"] + "/bin/testparm", "--suppress-prompt", str(root / "config/next/config/smb.conf")],
        check=True, stdout=-3, stderr=-3)
elif sys.argv[2] == "publish":
    native.publish(root)
else:
    if sys.argv[4] == "publish-partial":
        original_write = native.atomic_write
        def fail_mid_publication(path, data):
            if path == root / "config/passwd":
                raise OSError("injected local publication failure")
            original_write(path, data)
        with patch.object(native, "atomic_write", side_effect=fail_mid_publication):
            native.publish_config(root)
    else:
        native.publish_config(root)
`
	cmd := exec.CommandContext(ctx, r.python, "-B", "-c", bridge, native, command, root, r.failure)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Logf("native %s: %s", command, out)
	}
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
	runner := &smbStartupRunner{t: t, db: db, active: map[string]bool{}, readyRoles: map[string]bool{}, failure: failure, cancel: cancel, python: python, nonclustered: !clustered}
	oldRunner := common.ProcessExec
	common.ProcessExec = runner
	defer func() { common.ProcessExec = oldRunner }()
	root := t.TempDir()
	t.Setenv("SNAP_COMMON", root)
	t.Setenv("SNAP", "/test-snap")
	oldPaths := constants.GetPathConst
	constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: root} }
	defer func() { constants.GetPathConst = oldPaths }()
	require.NoError(t, os.WriteFile(filepath.Join(root, "ceph.conf"), []byte("[global]\n"), 0600))
	mockDB := &mocks.MockDB{TxFn: func(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
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
	state := interfaces.CephState{State: &mocks.MockState{ClusterName: "a", DBObj: mockDB}}
	err = enableSMBLocked(ctx, state, "alpha", true)
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
			want = []string{"key client.smb.fs.cluster.alpha", "key client.smb.cluster.alpha", "compile", "publish", "prepare-ctdb", "start ctdbd", "start ctdb-nodes", "ready ctdbd", "ready ctdb-nodes", "start smbd", "ready smbd", "start smbmetrics", "ready smbmetrics", "record"}
		}
		require.Equal(t, want, got)
		if python != "" {
			contextBytes, err := os.ReadFile(filepath.Join(runner.root, "config/context.json"))
			require.NoError(t, err)
			var launch struct {
				Clustered bool `json:"clustered"`
				SMBPort   int  `json:"smb_port"`
			}
			require.NoError(t, json.Unmarshal(contextBytes, &launch))
			require.Equal(t, clustered, launch.Clustered)
			require.Equal(t, 445, launch.SMBPort)
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
	require.NoError(t, db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
	require.Contains(t, raw, `"1":"a"`, "failure retains ownership and rank")
	for _, callback := range after {
		callback(state, runner)
	}
}
