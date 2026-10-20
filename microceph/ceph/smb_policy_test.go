package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

type smbPolicyRunner struct {
	calls    []string
	features string
	logical  string
}

func (r *smbPolicyRunner) RunCommand(name string, args ...string) (string, error) {
	return r.RunCommandContext(context.Background(), name, args...)
}
func (r *smbPolicyRunner) RunCommandContext(_ context.Context, name string, args ...string) (string, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if name == "ceph" {
		switch args[0] {
		case "mgr":
			return `{"enabled_modules":["smb"]}`, nil
		case "config":
			return "false", nil
		case "smb":
			return r.logical, nil
		}
	}
	if name == "rados" {
		return `{"service_type":"smb","service_id":"alpha","spec":{"cluster_id":"alpha","features":` + r.features + `}}`, nil
	}
	return "", fmt.Errorf("unexpected mutation")
}

func TestSMBInvalidADPolicyBeforeLocalStateOrOwner(t *testing.T) {
	for _, tc := range []struct{ features, mode, message string }{
		{`["domain"]`, "never", "Active Directory requires clustered SMB"},
		{`["domain"]`, "default", "Active Directory requires clustered SMB"},
		{`["clustered"]`, "always", "logical AD authentication does not match"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("SNAP_COMMON", root)
			runner := &smbPolicyRunner{features: tc.features, logical: `{"resource_type":"ceph.smb.cluster","cluster_id":"alpha","auth_mode":"active-directory","clustering":"` + tc.mode + `"}`}
			old := common.ProcessExec
			common.ProcessExec = runner
			defer func() { common.ProcessExec = old }()
			// No state/database is supplied: rejection must precede ownership access.
			err := enableSMBLocked(context.Background(), interfaces.CephState{}, "alpha", true)
			require.ErrorContains(t, err, tc.message)
			_, err = os.Stat(filepath.Join(root, "smb"))
			require.ErrorIs(t, err, os.ErrNotExist)
			for _, call := range runner.calls {
				require.NotContains(t, call, "auth ")
				require.NotContains(t, call, "snapctl")
			}
		})
	}
}

func TestSMBNativeEffectivePolicyRejectsOverrides(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, tc := range []struct {
		name      string
		clustered bool
		options   string
	}{
		{"security", false, `"security":"ads"`},
		{"security-ads-whitespace", false, `"security":"a d s"`},
		{"security-domain-whitespace", false, `"security":"d o m a i n"`},
		{"legacy-domain", true, `"security":"domain"`},
		{"realm", false, `"realm":"EXAMPLE.TEST"`},
		{"role", true, `"server role":"active directory domain controller"`},
		{"member", false, `"server role":"member"`},
		{"member-server", false, `"server role":"member  server"`},
		{"primary-controller", false, `"server role":"classic primary domain controller"`},
		{"backup-controller", false, `"server role":"classic backup domain controller"`},
		{"controller-alias", false, `"server role":"domain controller"`},
		{"dc-alias", false, `"server role":"DC"`},
		{"ipa-controller", false, `"server role":"IPA primary domain controller"`},
		{"role-no-space", false, `"serverrole":"active directory domain controller"`},
		{"role-mixed-whitespace", false, `" SeRvEr\tRo\fle ":"active directory domain controller"`},
		{"security-spelling", false, `"SEC URITY":"ads"`},
		{"realm-spelling", false, `"r e a l m":"EXAMPLE.TEST"`},
		{"ctdb-spelling", false, `"clu\u000bstering":"yes"`},
		{"ctdb-on", false, `"clustering":"yes"`},
		{"ctdb-off", true, `"clustering":"no"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := nativeTestCandidate(tc.clustered)
			var document map[string]any
			require.NoError(t, json.Unmarshal(candidate.Config, &document))
			var options map[string]any
			require.NoError(t, json.Unmarshal([]byte("{"+tc.options+"}"), &options))
			document["globals"] = map[string]any{"default": map[string]any{"options": options}}
			raw, err := json.Marshal(document)
			require.NoError(t, err)
			candidate.Config = raw
			_, err = projectSMBNative(candidate, filepath.Join(t.TempDir(), "alpha"), binding)
			require.Error(t, err, "contradictory mode must fail closed")
		})
	}
}

func TestSMBNativeEffectivePolicyAcceptsStandaloneRoles(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, role := range []string{"auto", "standalone server", "standalone", "standalone  server", "StAnDaLoNe\tSeRvEr", "standaloneserver", "a u t o"} {
		t.Run(role, func(t *testing.T) {
			candidate := nativeTestCandidate(false)
			options, err := json.Marshal(map[string]string{
				"server role":   role,
				"server string": "MiXeD  literal {{.value}}",
			})
			require.NoError(t, err)
			candidate.Config = json.RawMessage(strings.Replace(string(candidate.Config), `{"server string":"literal {{.value}}"}`, string(options), 1))
			root := filepath.Join(t.TempDir(), "alpha")
			plan, err := projectSMBNative(candidate, root, binding)
			require.NoError(t, err)
			conf := string(plan.Files["config/smb.conf"])
			require.Contains(t, conf, "server role = "+role, "validation must not rewrite rendered enum values")
			require.Contains(t, conf, "server string = MiXeD  literal {{.value}}", "string options are not enums")
		})
	}
}

func TestSMBNativeEffectivePolicyUsesFinalGlobals(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, tc := range []struct {
		name      string
		clustered bool
		first     string
		last      string
	}{
		{"ctdb-off", false, `"clustering":"yes"`, `"clustering":"no"`},
		{"ctdb-on", true, `"clustering":"no"`, `"clustering":"yes"`},
		{"ctdb-alias", false, `"clu stering":"yes"`, `"CLUSTERING":"no"`},
		{"ctdb-off-spaced", false, `"clu stering":"O N"`, `"CLUSTERING":"o f f"`},
		{"ctdb-on-spaced", true, `"clustering":"f a l s e"`, `"clu stering":"t r u e"`},
		{"ctdb-unknown-superseded", false, `"clustering":"unknown"`, `"clu stering":"o f f"`},
		{"security", false, `"sec urity":"ads"`, `"security":"user"`},
		{"realm", false, `"realm":"EXAMPLE.TEST"`, `"r e a l m":""`},
		{"role", false, `"serverrole":"active directory domain controller"`, `"server role":"standalone server"`},
		{"role-alias", false, `"serverrole":"member"`, `"server role":"standalone"`},
		{"domain-logons", false, `"domain logons":"yes"`, `"domainlogons":"off"`},
	} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse-%t", tc.name, reverse), func(t *testing.T) {
				candidate := nativeTestCandidate(tc.clustered)
				// Keep raw option order as well as the selected global-section order.
				config := strings.Replace(string(candidate.Config), `"globals":{"default":{"options":{"server string":"literal {{.value}}"}}}`, `"globals":{"default":{"options":{`+tc.first+`}},"override":{"options":{`+tc.last+`}}}`, 1)
				refs := `"globals":["default","override"]`
				if reverse {
					refs = `"globals":["override","default"]`
				}
				candidate.Config = json.RawMessage(strings.Replace(config, `"globals":["default"]`, refs, 1))
				plan, err := projectSMBNative(candidate, filepath.Join(t.TempDir(), "alpha"), binding)
				if reverse {
					require.ErrorContains(t, err, "effective", "final contradictory value must fail closed")
					return
				}
				require.NoError(t, err, "superseded value must not reject a consistent effective mode")
				var first, last map[string]string
				require.NoError(t, json.Unmarshal([]byte("{"+tc.first+"}"), &first))
				require.NoError(t, json.Unmarshal([]byte("{"+tc.last+"}"), &last))
				conf := string(plan.Files["config/smb.conf"])
				var firstLine, lastLine string
				for key, value := range first {
					firstLine = key + " = " + value
				}
				for key, value := range last {
					lastLine = key + " = " + value
				}
				require.Contains(t, conf, firstLine)
				require.Contains(t, conf, lastLine)
				require.Less(t, strings.Index(conf, firstLine), strings.Index(conf, lastLine), "rendering must retain upstream order")
			})
		}
	}
}

func TestSMBNativeEffectivePolicyUsesFinalAliasWithinSection(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse-%t", reverse), func(t *testing.T) {
			options := `"serverrole":"active directory domain controller","server role":"standalone  server"`
			if reverse {
				options = `"server role":"standalone  server","serverrole":"active directory domain controller"`
			}
			candidate := nativeTestCandidate(false)
			candidate.Config = json.RawMessage(strings.Replace(string(candidate.Config), `"server string":"literal {{.value}}"`, options, 1))
			_, err := projectSMBNative(candidate, filepath.Join(t.TempDir(), "alpha"), binding)
			if reverse {
				require.ErrorContains(t, err, "effective server role is not prepared")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestSMBLegacyWarningIsNotApplicationError(t *testing.T) {
	t.Setenv("SNAP_COMMON", t.TempDir())
	root := smbRoot("alpha")
	require.NoError(t, smbAtomicWrite(filepath.Join(smbBase(), "owner.json"), []byte("alpha")))
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "diagnostics", "policy-warning"), []byte("legacy group is policy-noncompliant")))
	old := smbReadyNativeFunc
	smbReadyNativeFunc = func(context.Context, string, string) error { return nil }
	defer func() { smbReadyNativeFunc = old }()
	status, err := SMBLocalStatus(context.Background())
	require.NoError(t, err)
	raw, err := json.Marshal(status)
	require.NoError(t, err)
	var value map[string]any
	require.NoError(t, json.Unmarshal(raw, &value))
	require.Equal(t, "legacy group is policy-noncompliant", value["policy_warning"])
	require.Empty(t, status.Error)
	require.True(t, status.Ready)
}

func TestSMBNativePolicyInspectionPrecedesReservation(t *testing.T) {
	python := os.Getenv("MICROCEPH_SMB_TEST_PYTHON")
	if python == "" {
		t.Skip("set MICROCEPH_SMB_TEST_PYTHON and pinned Sambacc PYTHONPATH")
	}
	for _, tc := range []struct{ failure, message string }{
		{"policy-override", "effective realm is not prepared"},
		{"policy-role-spelling", "effective server role is not prepared"},
		{"policy-security-spelling", "effective domain/security mode is not prepared"},
		{"policy-realm-spelling", "effective realm is not prepared"},
		{"policy-clustering-spelling", "effective clustering override conflicts"},
		{"policy-auto-pdc", "effective automatic server role is not prepared"},
		{"policy-auto-bdc", "effective automatic server role is not prepared"},
	} {
		t.Run(tc.failure, func(t *testing.T) {
			runSMBStartup(t, tc.failure, false, python, func(_ interfaces.StateInterface, r *smbStartupRunner) {
				require.ErrorContains(t, r.startupErr, tc.message)
				require.Empty(t, r.events, "no serving identity, compilation or role control before mode validation")
				var count int
				require.NoError(t, r.db.QueryRow("SELECT COUNT(*) FROM service_groups").Scan(&count))
				require.Zero(t, count)
			})
		})
	}
}

func TestSMBLegacyRetryWarnsWithoutShutdownAndReconciles(t *testing.T) {
	runSMBStartup(t, "", false, "", func(state interfaces.StateInterface, r *smbStartupRunner) {
		_, err := r.db.Exec(`INSERT INTO core_cluster_members VALUES(2,'b','192.0.2.2:7443')`)
		require.NoError(t, err)
		var raw string
		require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
		var config database.SMBServiceGroupConfig
		require.NoError(t, json.Unmarshal([]byte(raw), &config))
		config.Owners["2"] = "b"
		data, err := json.Marshal(config)
		require.NoError(t, err)
		_, err = r.db.Exec("UPDATE service_groups SET config=?", string(data))
		require.NoError(t, err)
		r.events = nil
		require.NoError(t, enableSMBLocked(context.Background(), state, "alpha", true))
		for _, event := range r.events {
			require.NotContains(t, event, "start ")
			require.NotContains(t, event, "stop ")
		}
		status, err := SMBLocalStatus(context.Background())
		require.NoError(t, err)
		require.Contains(t, status.PolicyWarning, "policy-noncompliant")
		require.Empty(t, status.Error)
		require.True(t, status.Ready)
		// The extra test owner has no apps; simulate its explicit verified retirement.
		tx, err := r.db.Begin()
		require.NoError(t, err)
		require.NoError(t, database.RetireSMBOwner(context.Background(), tx, "alpha", "b"))
		require.NoError(t, tx.Commit())
		// The lightweight compiler double stages context but does not publish it.
		contextBytes, err := os.ReadFile(filepath.Join(r.root, "config/next/config/context.json"))
		require.NoError(t, err)
		require.NoError(t, smbAtomicWrite(filepath.Join(r.root, "config/context.json"), contextBytes))
		r.refresh = true
		require.NoError(t, refreshSMBLocked(context.Background(), state))
		status, err = SMBLocalStatus(context.Background())
		require.NoError(t, err)
		require.Empty(t, status.PolicyWarning)
		require.Empty(t, status.Error)
		require.True(t, status.Ready)
	})
}

func TestSMBNonclusteredADDoesNotReadJoinSources(t *testing.T) {
	calls := []string{}
	_, err := readSMBCandidate("alpha", func(uri string) ([]byte, error) {
		calls = append(calls, uri)
		return []byte(`{"service_type":"smb","service_id":"alpha","spec":{"cluster_id":"alpha","features":["domain"],"join_sources":["rados:mon-config-key:smb/config/alpha/join"]}}`), nil
	})
	require.ErrorContains(t, err, "Active Directory requires clustered SMB")
	require.Equal(t, []string{"rados://.smb/alpha/spec.smb"}, calls)
}

func TestSMBDefaultPolicyUsesPinnedUpstreamMode(t *testing.T) {
	src := os.Getenv("MICROCEPH_SMB_TEST_CEPH_SRC")
	python := os.Getenv("MICROCEPH_SMB_TEST_PYTHON")
	if src == "" || python == "" {
		t.Skip("set MICROCEPH_SMB_TEST_CEPH_SRC to pinned Ceph src and MICROCEPH_SMB_TEST_PYTHON")
	}
	// Load the real pure resource module without the mgr's live Module entrypoint.
	code := `import json,sys,types,typing
from pathlib import Path
src=Path(sys.argv[1]); mgr=src/'pybind/mgr'
sys.path[:0]=[str(mgr),str(src/'python-common')]
pkg=types.ModuleType('smb');pkg.__path__=[str(mgr/'smb')];sys.modules['smb']=pkg
# object_format needs only this type alias, not a live Ceph mgr.
mgr_types=types.ModuleType('mgr_module');mgr_types.HandlerFuncType=typing.Callable;sys.modules['mgr_module']=mgr_types
from smb.resources import Cluster
from smb.enums import AuthMode, SMBClustering
from ceph.deployment.service_spec import PlacementSpec
rows=[]
for mode,count in [('default',None),('default',1),('default',2),('always',1),('never',2)]:
 c=Cluster(cluster_id='alpha',auth_mode=AuthMode.USER,clustering=SMBClustering(mode),placement=PlacementSpec(count=count))
 rows.append(dict(mode=mode,count=count,clustered=c.is_clustered()))
print(json.dumps(rows))`
	out, err := exec.Command(python, "-B", "-c", code, src).CombinedOutput()
	require.NoError(t, err, string(out))
	var rows []struct {
		Mode      string `json:"mode"`
		Count     *int   `json:"count"`
		Clustered bool   `json:"clustered"`
	}
	require.NoError(t, json.Unmarshal(out, &rows))
	require.Len(t, rows, 5)
	binding := nativeTestBinding(t)
	for _, row := range rows {
		logical, err := json.Marshal(map[string]any{"auth_mode": "user", "clustering": row.Mode, "placement": map[string]any{"count": row.Count}})
		require.NoError(t, err)
		candidate := nativeTestCandidate(row.Clustered)
		require.NoError(t, validateSMBLogicalPolicy(logical, candidate.Spec))
		_, err = projectSMBNative(candidate, filepath.Join(t.TempDir(), "alpha"), binding)
		require.NoError(t, err)
	}
}

func TestSMBNativeClusteredPolicyRejectsIncludes(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, tc := range []struct {
		name, anchor, key string
		member            bool
	}{
		{"ad-global-alias", `"security":"ads"`, "in clude", true},
		{"ad-share", `"path":"/data"`, "include", true},
		{"ad-share-alias", `"path":"/data"`, " In\tClUdE ", true},
		{"user-global", `"server string":"literal {{.value}}"`, "include", false},
		{"user-share-alias", `"path":"/data"`, "in clude", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := nativeTestCandidate(true)
			if tc.member {
				candidate = nativeADCandidate()
			}
			option, err := json.Marshal(map[string]string{tc.key: "/private/override.conf"})
			require.NoError(t, err)
			candidate.Config = json.RawMessage(strings.Replace(string(candidate.Config), tc.anchor, tc.anchor+","+strings.Trim(string(option), "{}"), 1))
			var document map[string]any
			require.NoError(t, json.Unmarshal(candidate.Config, &document))
			var description smbBindingDescription
			require.NoError(t, binding("inspect", smbBindingRequest{ClusterID: "alpha", Config: document}, &description))
			t.Run("admission", func(t *testing.T) {
				_, err := smbMemberContext(candidate, description)
				require.ErrorContains(t, err, "include", "shared admission must reject before reservation")
			})
			t.Run("compiler", func(t *testing.T) {
				_, err := projectSMBNative(candidate, "/native/alpha", binding)
				require.ErrorContains(t, err, "include", "compiler must share the admission guard")
			})
			if tc.member {
				t.Run("enable", func(t *testing.T) {
					candidate.Spec.ConfigURI = "rados://.smb/alpha/config.smb"
					runSMBStartupFixture(t, "policy-include", true, os.Getenv("MICROCEPH_SMB_TEST_PYTHON"), &candidate, func(_ interfaces.StateInterface, r *smbStartupRunner) {
						require.ErrorContains(t, r.startupErr, "include")
						require.Empty(t, r.events, "no serving identity, compilation, role control or join")
						var count int
						require.NoError(t, r.db.QueryRow("SELECT COUNT(*) FROM service_groups").Scan(&count))
						require.Zero(t, count)
					})
				})
			}
		})
	}
}

func TestSMBNativeIncludePolicyPreservesUnselectedAndNonclusteredOptions(t *testing.T) {
	binding := nativeTestBinding(t)
	candidate := nativeADCandidate()
	candidate.Config = json.RawMessage(strings.Replace(string(candidate.Config), `"globals":{"default":`, `"globals":{"unused":{"options":{"in clude":"/private/unused.conf"}},"default":`, 1))
	candidate.Config = json.RawMessage(strings.Replace(string(candidate.Config), `"shares":{"data":`, `"shares":{"unused":{"options":{"include":"/private/unused.conf"}},"data":`, 1))
	plan, err := projectSMBNative(candidate, "/native/alpha", binding)
	require.NoError(t, err)
	require.NotContains(t, string(plan.Files["config/smb.conf"]), "unused.conf")
	candidate = nativeTestCandidate(false)
	candidate.Config = json.RawMessage(strings.Replace(string(candidate.Config), `"path":"/data"`, `"path":"/data","include":"/private/legacy.conf"`, 1))
	plan, err = projectSMBNative(candidate, "/native/alpha", binding)
	require.NoError(t, err)
	require.Contains(t, string(plan.Files["config/smb.conf"]), "include = /private/legacy.conf")
}

func TestSMBNativeEffectivePolicyDerivesAutomaticRole(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, tc := range []struct {
		name, options  string
		member, reject bool
	}{
		{"omitted-pdc", `"security":"user","domain logons":"yes","domain master":"yes"`, false, true},
		{"auto-bdc", `"security":"auto","server role":"auto","domain logons":"1","domain master":"no"`, false, true},
		{"defaults-pdc", `"domain logons":"on"`, false, true},
		{"auto-enums", `"server role":"a u t o","domainlogons":"t r u e","domain master":"a u t o"`, false, true},
		{"standalone-explicit", `"server role":"standalone","domain logons":"yes","domain master":"yes"`, false, false},
		{"not-logons", `"domain logons":"o f f","domain master":"yes"`, false, false},
		{"member-auto", `"domain logons":"yes","domain master":"no"`, true, false},
		{"member-explicit", `"server role":"member","domain logons":"yes","domain master":"yes"`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := nativeTestCandidate(false)
			if tc.member {
				candidate = nativeADCandidate()
				candidate.Config = json.RawMessage(strings.Replace(string(candidate.Config), `"security":"ads"`, `"security":"ads",`+tc.options, 1))
			} else {
				candidate.Config = json.RawMessage(strings.Replace(string(candidate.Config), `"server string":"literal {{.value}}"`, tc.options, 1))
			}
			plan, err := projectSMBNative(candidate, "/native/alpha", binding)
			if tc.reject {
				require.ErrorContains(t, err, "effective automatic server role is not prepared")
				return
			}
			require.NoError(t, err)
			var options map[string]string
			require.NoError(t, json.Unmarshal([]byte("{"+tc.options+"}"), &options))
			for key, value := range options {
				require.Contains(t, string(plan.Files["config/smb.conf"]), key+" = "+value, "keep upstream options, do not silently disable them")
			}
		})
	}
}

func TestSMBNativeEffectivePolicyAcceptsSecurityEnums(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, security := range []string{"USER", "u s e r", "U\tSe R", "AUTO", "a u t o", "A\tU TO"} {
		t.Run(security, func(t *testing.T) {
			candidate := nativeTestCandidate(false)
			options, err := json.Marshal(map[string]string{"security": security, "server string": "MiXeD  literal {{.value}}"})
			require.NoError(t, err)
			candidate.Config = json.RawMessage(strings.Replace(string(candidate.Config), `{"server string":"literal {{.value}}"}`, string(options), 1))
			plan, err := projectSMBNative(candidate, "/native/alpha", binding)
			require.NoError(t, err)
			conf := string(plan.Files["config/smb.conf"])
			require.Contains(t, conf, "security = "+security, "validate enums without rewriting rendered values")
			require.Contains(t, conf, "server string = MiXeD  literal {{.value}}")
		})
	}
}

// smbPolicyNamedShare keeps the exact renderer input, including section names
// that Samba interprets as globals rather than as ordinary shares.
func smbPolicyNamedShare(t *testing.T, candidate smbCandidate, name string) smbCandidate {
	t.Helper()
	encoded, err := json.Marshal(name)
	require.NoError(t, err)
	config := strings.ReplaceAll(string(candidate.Config), `"data"`, string(encoded))
	candidate.Config = json.RawMessage(strings.Replace(config, `"path":"/data"`, `"path":"/data","netbios name":"OTHER","idmap config * : range":"300000-399999"`, 1))
	return candidate
}

func TestSMBNativePolicyRejectsReservedGlobalShares(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, name := range []string{"global", "globals", "G l O b A l", " \tGlObAlS\t ", "g\vl\fo\tb a l s"} {
		for _, member := range []bool{false, true} {
			t.Run(fmt.Sprintf("%q/member-%t", name, member), func(t *testing.T) {
				candidate := nativeTestCandidate(true)
				if member {
					candidate = nativeADCandidate()
				}
				candidate = smbPolicyNamedShare(t, candidate, name)
				var document map[string]any
				require.NoError(t, json.Unmarshal(candidate.Config, &document))
				request := smbBindingRequest{ClusterID: "alpha", Config: document}
				var description smbBindingDescription
				require.NoError(t, binding("inspect", request, &description))
				// The real renderer still permits a later global section. Native
				// validation, not a rewritten upstream renderer, must guard it.
				var rendered struct {
					Config string `json:"smb_conf"`
				}
				require.NoError(t, binding("render", request, &rendered))
				section := strings.LastIndex(rendered.Config, "["+name+"]")
				require.Positive(t, section)
				require.Greater(t, strings.Index(rendered.Config, "netbios name = OTHER"), section)
				require.Greater(t, strings.Index(rendered.Config, "idmap config * : range = 300000-399999"), section)
				t.Run("admission", func(t *testing.T) {
					_, err := smbMemberContext(candidate, description)
					require.ErrorContains(t, err, "reserved global section")
				})
				t.Run("compiler", func(t *testing.T) {
					plan, err := projectSMBNative(candidate, "/native/alpha", binding)
					if err == nil {
						conf := string(plan.Files["config/smb.conf"])
						for _, line := range strings.Split(conf, "\n") {
							if strings.HasPrefix(line, "[") || strings.Contains(line, "netbios name =") || strings.Contains(line, "idmap config * : range =") {
								t.Log(line)
							}
						}
						if member {
							require.Equal(t, "ALPHA", plan.Launch.AD.NetBIOSName)
						}
					}
					require.ErrorContains(t, err, "reserved global section")
				})
				if member {
					t.Run("enable", func(t *testing.T) {
						candidate.Spec.ConfigURI = "rados://.smb/alpha/config.smb"
						runSMBStartupFixture(t, "policy-reserved-share", true, os.Getenv("MICROCEPH_SMB_TEST_PYTHON"), &candidate, func(_ interfaces.StateInterface, r *smbStartupRunner) {
							require.ErrorContains(t, r.startupErr, "reserved global section")
							require.Empty(t, r.events, "no identity provisioning, compilation, role control or AD join")
							var count int
							require.NoError(t, r.db.QueryRow("SELECT COUNT(*) FROM service_groups").Scan(&count))
							require.Zero(t, count, "no owner or AD bootstrap reservation")
							for _, path := range []string{filepath.Join(smbBase(), "owner.json"), filepath.Join(smbRoot("alpha"), "ad-initializing"), filepath.Join(smbRoot("alpha"), "config/candidate.json")} {
								_, err := os.Stat(path)
								require.ErrorIs(t, err, os.ErrNotExist)
							}
						})
					})
				}
			})
		}
	}
}

func TestSMBNativeReservedSharePolicyCompatibility(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, name := range []string{"data", "global-data", "myglobals", "globals$", "Global Archive"} {
		t.Run(name, func(t *testing.T) {
			candidate := nativeADCandidate()
			encoded, err := json.Marshal(name)
			require.NoError(t, err)
			candidate.Config = json.RawMessage(strings.ReplaceAll(string(candidate.Config), `"data"`, string(encoded)))
			plan, err := projectSMBNative(candidate, "/native/alpha", binding)
			require.NoError(t, err)
			require.Contains(t, string(plan.Files["config/smb.conf"]), "["+name+"]")
			require.Equal(t, "ALPHA", plan.Launch.AD.NetBIOSName)
		})
	}
	// Only selected sections matter. Preserve historical nonclustered parsing
	// (including allowed includes), without claiming AD identity safety there.
	candidate := nativeADCandidate()
	candidate.Config = json.RawMessage(strings.Replace(string(candidate.Config), `"shares":{"data":`, `"shares":{"global":{"options":{"netbios name":"OTHER"}},"data":`, 1))
	plan, err := projectSMBNative(candidate, "/native/alpha", binding)
	require.NoError(t, err)
	require.NotContains(t, string(plan.Files["config/smb.conf"]), "OTHER")
	candidate = smbPolicyNamedShare(t, nativeTestCandidate(false), "globals")
	candidate.Config = json.RawMessage(strings.Replace(string(candidate.Config), `"path":"/data"`, `"path":"/data","include":"/private/legacy.conf"`, 1))
	plan, err = projectSMBNative(candidate, "/native/alpha", binding)
	require.NoError(t, err)
	require.Contains(t, string(plan.Files["config/smb.conf"]), "[globals]")
	require.Contains(t, string(plan.Files["config/smb.conf"]), "include = /private/legacy.conf")
}

func TestSMBNativeEffectivePolicyClusteringBooleans(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, clustered := range []bool{false, true} {
		for _, tc := range []struct {
			value string
			on    bool
			valid bool
		}{
			{"yes", true, true}, {"true", true, true}, {"on", true, true}, {"1", true, true},
			{"YeS", true, true}, {"t r u e", true, true}, {"O\tN", true, true}, {" \t1\v\f", true, true},
			{"no", false, true}, {"false", false, true}, {"off", false, true}, {"0", false, true},
			{"o f f", false, true}, {"n o", false, true}, {"f a l s e", false, true}, {"OfF", false, true}, {"o\tf\vf\f", false, true},
			{"", false, false}, {"auto", false, false}, {"unknown", false, false}, {"2", false, false}, {"o\u00a0ff", false, false},
		} {
			t.Run(fmt.Sprintf("clustered-%t/%q", clustered, tc.value), func(t *testing.T) {
				candidate := nativeTestCandidate(clustered)
				options, err := json.Marshal(map[string]string{"clustering": tc.value, "server string": "MiXeD  literal {{.value}}"})
				require.NoError(t, err)
				candidate.Config = json.RawMessage(strings.Replace(string(candidate.Config), `{"server string":"literal {{.value}}"}`, string(options), 1))
				plan, err := projectSMBNative(candidate, "/native/alpha", binding)
				if !tc.valid || tc.on != clustered {
					require.ErrorContains(t, err, "effective clustering override conflicts")
					return
				}
				require.NoError(t, err)
				conf := string(plan.Files["config/smb.conf"])
				require.Contains(t, conf, "clustering = "+tc.value, "Boolean validation must not rewrite input")
				require.Contains(t, conf, "server string = MiXeD  literal {{.value}}", "arbitrary strings are not Boolean values")
				require.Equal(t, clustered, plan.Launch.Clustered)
			})
		}
	}
}

func TestSMBADJoinRejectsReservedGlobalSharesBeforeEffect(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, name := range []string{"global", "globals", "G l O b A l"} {
		t.Run(name, func(t *testing.T) {
			useSMBNativeProcesses(t)
			root, snap := smbADProcessFixture(t)
			candidate := nativeADCandidate()
			plan, err := projectSMBNative(candidate, root, binding)
			require.NoError(t, err)
			for _, path := range []string{"config/context.json", "config/smb.conf"} {
				require.NoError(t, writeSMBNativeFile(filepath.Join(root, path), plan.Files[path]))
			}
			// A changed private input must be revalidated even with a valid
			// previously compiled launch identity. No real Samba is executed.
			candidate = smbPolicyNamedShare(t, candidate, name)
			raw, err := json.Marshal(candidate)
			require.NoError(t, err)
			require.NoError(t, writeSMBNativeFile(filepath.Join(root, "config/candidate.json"), raw))
			require.NoError(t, os.Symlink(os.Getenv("MICROCEPH_SMB_TEST_PYTHON"), filepath.Join(snap, "bin/python3")))
			native, err := os.ReadFile("../../snapcraft/smb/native.py")
			require.NoError(t, err)
			require.NoError(t, writeSMBNativeFile(filepath.Join(snap, "smb/native.py"), native))
			require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/ctdb"), []byte("#!/bin/sh\n[ \"$5\" = getdbmap ] || exit 7\necho 'Number of databases:0'\n"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/net"), []byte("#!/bin/sh\ntouch \"$SNAP/join-attempted\"\nexit 1\n"), 0700))
			err = runSMBADJoin(context.Background(), root)
			_, statErr := os.Stat(filepath.Join(snap, "join-attempted"))
			if !os.IsNotExist(statErr) {
				t.Error("reserved section reached the join process")
			}
			require.ErrorContains(t, err, "reserved global section")
			_, err = os.Stat(filepath.Join(root, "ad-initializing"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}
