package ceph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

type smbLocalStartupRunner struct {
	t          *testing.T
	db         *sql.DB
	failure    string
	active     bool
	clustered  bool
	ctdbActive bool
	commands   []string
	spec       string
	config     string
	users      string
	disabled   bool
	unready    bool
	logical    string
	onControl  func(context.Context, string) error
}

func (r *smbLocalStartupRunner) RunCommand(name string, args ...string) (string, error) {
	return r.RunCommandContext(context.Background(), name, args...)
}

func (r *smbLocalStartupRunner) RunCommandContext(ctx context.Context, name string, args ...string) (string, error) {
	r.commands = append(r.commands, name+" "+strings.Join(args, " "))
	if name == "ceph" {
		switch args[0] {
		case "mgr":
			return `{"enabled_modules":["smb"]}`, nil
		case "config":
			return "false", nil
		case "smb":
			if r.logical != "" {
				return r.logical, nil
			}
			return `{"resource_type":"ceph.smb.cluster","cluster_id":"alpha","auth_mode":"user","clustering":"never"}`, nil
		case "auth":
			if args[2] != "client.smb.config.cluster.alpha" {
				var raw string
				require.NoError(r.t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
				require.Contains(r.t, raw, `"1":"a"`, "ownership precedes data identity")
			}
			return `[{"entity":"` + args[2] + `","key":"AQAAAA=="}]`, nil
		}
	}
	if name == "rados" {
		if r.failure == "source-failure" {
			return "", errors.New("source transport failed")
		}
		var data string
		switch args[len(args)-2] {
		case "spec.smb":
			data = r.spec
		case "config.smb":
			data = r.config
		case "users.json":
			data = r.users
		default:
			return "", errors.New("Error ENOENT")
		}
		if args[len(args)-1] == "-" {
			return data, nil
		}
		return "", os.WriteFile(args[len(args)-1], []byte(data), 0600)
	}
	if name == "/test-snap/bin/testparm" {
		if r.failure == "source-change" {
			r.config = strings.Replace(r.config, `"globals":{}`, `"globals":{"changed":{"options":{}}}`, 1)
			r.config = strings.Replace(r.config, `"server string":"updated"`, `"server string":"torn"`, 1)
		}
		if r.failure == "compile" {
			return "", errors.New("source-password-private")
		}
		return "", nil
	}
	if name == "/test-snap/bin/python3" {
		if args[2] == "render" && r.failure == "binding" {
			return "", errors.New("source-password-private")
		}
		if args[2] == "check-data" {
			// CephFS transport is modeled here; projection/rendering is real.
			return "{}", nil
		}
		if args[2] == "accounts" {
			require.False(r.t, r.active, "SAM import requires confirmed stopped smbd")
			if r.failure == "accounts" {
				// Model runSMBPrivateCommand's public result, not raw stderr.
				return "", errors.New("SMB native command failed; inspect private diagnostics; external outcome may be uncertain")
			}
			// Native PassDBLoader creates both databases, even with no users.
			for _, name := range []string{"passdb.tdb", "secrets.tdb"} {
				path := filepath.Join(smbRoot("alpha"), "samba/private", name)
				_, err := os.Stat(path)
				if errors.Is(err, os.ErrNotExist) {
					require.NoError(r.t, os.WriteFile(path, []byte("retained-native-identity"), 0600))
				}
			}
			return "{}", nil
		}
		path, err := filepath.Abs("../../snapcraft/smb/native.py")
		if err != nil {
			return "", err
		}
		out, err := exec.CommandContext(ctx, os.Getenv("MICROCEPH_SMB_TEST_PYTHON"), "-B", path, args[2], args[3]).Output()
		return string(out), err
	}
	if name == "/test-snap/bin/ctdb" && r.clustered {
		launch, err := loadSMBLaunch(smbRoot("alpha"))
		if err != nil {
			return "", err
		}
		switch args[0] {
		case "pnn":
			return strconv.FormatUint(*launch.CTDBPNN, 10), nil
		case "nodestatus", "reloadnodes":
			return "", nil
		case "status":
			var candidate smbCandidate
			raw, err := os.ReadFile(filepath.Join(smbRoot("alpha"), "config/candidate.json"))
			if err != nil {
				return "", err
			}
			err = json.Unmarshal(raw, &candidate)
			if err != nil {
				return "", err
			}
			out := fmt.Sprintf("Number of nodes:%d\n", candidate.CTDB.SlotCount)
			for _, m := range append([]database.SMBCTDBMember{candidate.CTDB.Member}, candidate.CTDB.Peers...) {
				out += fmt.Sprintf("pnn:%d %s OK\n", m.PNN, m.Node)
			}
			return out + "Recovery mode:NORMAL (0)\n", nil
		}
	}
	if name == "snapctl" {
		require.Contains(r.t, []string{"microceph.smbd", "microceph.ctdbd"}, args[1], "only installed roles can be controlled")
		if args[1] == "microceph.ctdbd" {
			if !r.clustered {
				require.Contains(r.t, []string{"stop", "services"}, args[0], "local-only fixture must never start CTDB")
				return "microceph.ctdbd disabled inactive", nil
			}
			switch args[0] {
			case "start", "restart":
				r.ctdbActive = true
			case "stop":
				r.ctdbActive = false
			}
			activity := "inactive"
			if r.ctdbActive {
				activity = "active"
			}
			return "microceph.ctdbd enabled " + activity, nil
		}
		if r.onControl != nil {
			err := r.onControl(ctx, args[0])
			if err != nil {
				return "", err
			}
		}
		switch args[0] {
		case "restart":
			if r.failure == "restart" {
				return "", errors.New("uncertain restart")
			}
			r.active = true
		case "start":
			r.active = true
			if r.failure == "start" {
				return "", errors.New("uncertain start")
			}
		case "stop":
			if r.failure == "stop" {
				return "", errors.New("uncertain stop")
			}
			r.active = false
		case "services":
			activity := "inactive"
			if r.active {
				activity = "active"
			}
			startup := "enabled"
			if r.disabled {
				startup = "disabled"
			}
			return args[1] + " " + startup + " " + activity, nil
		}
		return "", nil
	}
	return "", fmt.Errorf("unexpected fixture command %s", name)
}

func smbLocalStartupFixture(t *testing.T, failure string) (interfaces.StateInterface, *smbLocalStartupRunner) {
	t.Helper()
	if os.Getenv("MICROCEPH_SMB_TEST_PYTHON") == "" {
		t.Skip("requires hash-pinned Sambacc interpreter and PYTHONPATH")
	}
	ctx := context.Background()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "startup.db")+"?_foreign_keys=on")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`CREATE TABLE core_cluster_members(id INTEGER PRIMARY KEY, name TEXT UNIQUE, address TEXT NOT NULL); INSERT INTO core_cluster_members VALUES(1,'a','192.0.2.1:7443');`)
	require.NoError(t, err)
	tx, err := db.Begin()
	require.NoError(t, err)
	require.NoError(t, database.SchemaExtensions[5](ctx, tx))
	require.NoError(t, tx.Commit())
	runner := &smbLocalStartupRunner{t: t, db: db, failure: failure, spec: `{"service_type":"smb","service_id":"alpha","spec":{"cluster_id":"alpha","config_uri":"rados://.smb/alpha/config.smb","bind_addrs":[{"address":"192.0.2.1"}]}}`, config: `{"samba-container-config":"v0","configs":{"alpha":{"globals":[],"shares":[]}},"globals":{},"shares":{}}`}
	root := t.TempDir()
	t.Setenv("SNAP_COMMON", root)
	t.Setenv("SNAP", "/test-snap")
	oldRunner, oldNetwork, oldPaths, oldReady, oldPrivate := common.ProcessExec, common.Network, constants.GetPathConst, smbReadyNativeFunc, runSMBPrivateCommandFunc
	t.Cleanup(func() {
		common.ProcessExec, common.Network, constants.GetPathConst, smbReadyNativeFunc, runSMBPrivateCommandFunc = oldRunner, oldNetwork, oldPaths, oldReady, oldPrivate
	})
	common.ProcessExec = runner
	common.Network = smbEndpointNetwork{addresses: []string{"192.0.2.1"}}
	constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: root} }
	require.NoError(t, os.WriteFile(filepath.Join(root, "ceph.conf"), []byte("[global]\n"), 0600))
	smbReadyNativeFunc = func(context.Context, string, string) error {
		if !runner.active || runner.unready || failure == "ready" {
			return errors.New("not ready")
		}
		return nil
	}
	runSMBPrivateCommandFunc = func(ctx context.Context, root string, argv []string, input io.Reader) ([]byte, error) {
		out, err := runner.RunCommandContext(ctx, argv[0], argv[1:]...)
		return []byte(out), err
	}
	mockDB := &mocks.MockDB{TxFn: func(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		err = fn(ctx, tx)
		if err != nil {
			return err
		}
		var count int
		require.NoError(t, tx.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
		if count > 0 && failure == "record" {
			return errors.New("publication commit failed")
		}
		return tx.Commit()
	}}
	return interfaces.CephState{State: &mocks.MockState{ClusterName: "a", DBObj: mockDB}}, runner
}

func TestSMBLocalStartupSQLOrderAndCompensation(t *testing.T) {
	for _, failure := range []string{"success", "compile", "binding", "source-change", "accounts", "start", "record", "stop"} {
		t.Run(failure, func(t *testing.T) {
			if failure == "success" {
				failure = ""
			}
			state, runner := smbLocalStartupFixture(t, failure)
			err := enableSMBLocked(context.Background(), state, "alpha")
			var count int
			require.NoError(t, runner.db.QueryRow("SELECT COUNT(*) FROM grouped_services").Scan(&count))
			if failure == "" {
				require.NoError(t, err)
				require.Equal(t, 1, count)
				launch, err := loadSMBLaunch(smbRoot("alpha"))
				require.NoError(t, err)
				require.True(t, launch.Startup["smbd"].Prepared)
				require.True(t, runner.active)
				require.NoError(t, DisableSMB(context.Background(), state, "alpha", false))
				require.False(t, runner.active)
				_, err = os.Stat(smbRoot("alpha"))
				require.NoError(t, err, "nonpurge disable retains SAM/SID and config")
				require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
				require.NoError(t, DisableSMB(context.Background(), state, "alpha", true))
				_, err = os.Stat(smbRoot("alpha"))
				require.ErrorIs(t, err, os.ErrNotExist)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "source-password-private")
				if failure == "compile" || failure == "binding" {
					require.Contains(t, err.Error(), "inspect private cluster diagnostics")
					path := filepath.Join(smbRoot("alpha"), "diagnostics/native-error")
					diagnostic, readErr := os.ReadFile(path)
					require.NoError(t, readErr)
					require.Contains(t, string(diagnostic), "source-password-private")
					info, statErr := os.Stat(path)
					require.NoError(t, statErr)
					require.Equal(t, os.FileMode(0600), info.Mode().Perm())
				}
				require.Zero(t, count)
				require.False(t, runner.active)
				var raw string
				require.NoError(t, runner.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
				require.Contains(t, raw, `"1":"a"`, "failure never erases ownership uncertainty")
				if failure == "stop" {
					require.Error(t, checkSMBStartIntent(smbRoot("alpha")))
				}
			}
		})
	}
}

func TestSMBLocalPolicyAdmissionPrecedesOwnershipAndServingIdentity(t *testing.T) {
	for _, mode := range []string{"clustered", "domain", "cephfs-proxy", "future", "effective-domain"} {
		t.Run(mode, func(t *testing.T) {
			state, runner := smbLocalStartupFixture(t, "")
			if mode == "effective-domain" {
				runner.config = strings.Replace(runner.config, `"globals":{}`, `"globals":{"policy":{"options":{"security":"ads"}}}`, 1)
				runner.config = strings.Replace(runner.config, `"globals":[]`, `"globals":["policy"]`, 1)
			} else {
				runner.spec = strings.Replace(runner.spec, `"spec":{`, `"spec":{"features":["`+mode+`"],`, 1)
			}
			try := enableSMBLocked(context.Background(), state, "alpha")
			require.Error(t, try)
			var count int
			require.NoError(t, runner.db.QueryRow("SELECT COUNT(*) FROM service_groups").Scan(&count))
			require.Zero(t, count)
			for _, command := range runner.commands {
				require.NotContains(t, command, "client.smb.fs.cluster.alpha")
				require.NotContains(t, command, "snapctl")
			}
			_, err := os.Stat(filepath.Join(smbBase(), "owner.json"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestSMBAccountPreparationBudgetAndCancellation(t *testing.T) {
	state, _ := smbLocalStartupFixture(t, "")
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
	old := runSMBPrivateCommandFunc
	t.Cleanup(func() { runSMBPrivateCommandFunc = old })
	runSMBPrivateCommandFunc = func(ctx context.Context, _ string, _ []string, _ io.Reader) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 30*time.Second)
		return []byte("{}"), nil
	}
	require.NoError(t, prepareSMBAccounts(context.Background(), smbRoot("alpha"), false))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, prepareSMBAccounts(ctx, smbRoot("alpha"), false), context.Canceled)
}

func TestSMBPreparedContextRejectsFutureState(t *testing.T) {
	for _, raw := range []string{`{} {"clustered":true}`, `{} trailing`, `{"clustered":true}`, `{"ad_member":{}}`, `{"startup":{"smbd":{"initialized":true}}}`, `{"startup":{"smbmetrics":{}}}`} {
		_, err := decodeSMBLaunch([]byte(raw))
		require.Error(t, err)
	}
	var launch smbLaunch
	require.NoError(t, json.Unmarshal([]byte(`{"startup":{"smbd":{"prepared":true}}}`), &launch))
	require.True(t, launch.Startup["smbd"].Prepared)
}
