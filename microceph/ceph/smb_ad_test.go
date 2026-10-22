package ceph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
)

func nativeADCandidate() smbCandidate {
	c := nativeTestCandidate(true)
	c.Spec.Features = append(c.Spec.Features, "domain")
	c.Spec.JoinSources = []string{"rados:mon-config-key:smb/config/alpha/join-a", "rados:mon-config-key:smb/config/alpha/join-b"}
	c.Joins = []json.RawMessage{json.RawMessage(`{"username":"joiner-a","password":"first-private"}`), json.RawMessage(`{"username":"joiner-b","password":"second-private"}`)}
	c.Users = nil
	c.Logical = json.RawMessage(`{"auth_mode":"active-directory","domain_settings":{"realm":"EXAMPLE.TEST"}}`)
	c.Config = json.RawMessage(strings.Replace(string(c.Config), `"server string":"literal {{.value}}"`, `"security":"ads","realm":"EXAMPLE.TEST","workgroup":"EXAMPLE","idmap config * : backend":"autorid","idmap config * : range":"2000-9999999"`, 1))
	return c
}

func TestSMBADMemberProjection(t *testing.T) {
	c := nativeADCandidate()
	plan, err := projectSMBNative(c, "/native/alpha", nativeTestBinding(t))
	require.NoError(t, err)
	conf := string(plan.Files["config/smb.conf"])
	for _, option := range []string{"security = ads", "realm = EXAMPLE.TEST", "netbios name = alpha", "workgroup = EXAMPLE", "idmap config * : backend = autorid", "winbindd socket directory = /run/samba/winbindd", "ctdb:secrets.tdb = yes", "ctdb:autorid.tdb = yes", "kerberos method = secrets only"} {
		require.Contains(t, conf, option)
	}
	require.Equal(t, "passwd: files winbind\ngroup: files winbind\nhosts: files dns\nservices: files\n", string(plan.Files["config/nsswitch.conf"]))
	require.NotContains(t, string(plan.Files["config/passwd"]), "joiner")
	require.NotContains(t, string(plan.Files["config/context.json"]), "private")
	require.NotContains(t, conf, "first-private")
	require.NotContains(t, plan.Files, "config/krb5.conf", "inherit Kerberos unless demonstrated insufficient")
	require.NotContains(t, plan.Files, "config/resolv.conf")
	var saved smbCandidate
	require.NoError(t, json.Unmarshal(plan.Files["config/candidate.json"], &saved))
	require.Equal(t, c.Joins, saved.Joins, "private inputs retain exact upstream order")
	// Only the complete clustered member role path is supported.
	roles, err := smbCoreRoles(c.Spec, plan.Launch.Proxy)
	require.NoError(t, err)
	require.Contains(t, roles, "winbindd")
}

func TestSMBADMemberRejectsContradictions(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, tc := range []struct{ name, old, value string }{
		{"controller", `["ctdb"]`, `["ctdb","addc"]`},
		{"legacy", `"security":"ads"`, `"security":"domain"`},
		{"standalone", `"security":"ads"`, `"security":"ads","serverrole":"standalone"`},
		{"realm", `"realm":"EXAMPLE.TEST"`, `"realm":"OTHER.TEST"`},
		{"machine", `"instance_name":"alpha"`, `"instance_name":"another"`},
		{"alias", `"security":"ads"`, `"security":"ads","netbios aliases":"alias"`},
		{"idmap", `"autorid"`, `"rid"`},
		{"local-secrets", `"security":"ads"`, `"security":"ads","ctdb:secrets.tdb":"no"`},
		{"local-autorid", `"security":"ads"`, `"security":"ads","ctdb:autorid.tdb":"no"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := nativeADCandidate()
			c.Config = json.RawMessage(strings.Replace(string(c.Config), tc.old, tc.value, 1))
			_, err := projectSMBNative(c, "/native/alpha", binding)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "first-private")
		})
	}
}

func TestSMBADJoinInputValidation(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, tc := range []struct{ name, raw string }{
		{"missing", `{}`}, {"type", `{"username":7,"password":"private"}`},
		{"empty", `{"username":"","password":"private"}`},
		{"stdin", `{"username":"joiner","password":"private\nextra"}`},
		{"argv", `{"username":"joiner%embedded-secret","password":"private"}`},
		{"nul", `{"username":"joiner\u0000","password":"private"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := nativeADCandidate()
			c.Joins[0] = json.RawMessage(tc.raw)
			_, err := projectSMBNative(c, "/native/alpha", binding)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private")
		})
	}
}

func TestSMBADPrivateRoleBindingsRemainFailClosed(t *testing.T) {
	c := nativeADCandidate()
	plan, err := projectSMBNative(c, "/native/alpha", nativeTestBinding(t))
	require.NoError(t, err)
	for _, role := range []string{"smbd", "winbindd"} {
		require.False(t, plan.Launch.Startup[role].Prepared)
		require.Len(t, plan.Launch.Startup[role].Records, 3)
	}
	startup, err := os.ReadFile("../../snapcraft/commands/smb-startup")
	require.NoError(t, err)
	require.Contains(t, string(startup), `smb_bind_private "$SMB_ROOT/samba/winbindd" /run/samba/winbindd`)
	require.Contains(t, string(startup), `smb_bind_private "$SMB_ROOT/ctdb/run" /run/ctdb`)
	require.NotContains(t, string(startup), "SELFTEST_WINBINDD_SOCKET_DIR")
	base := t.TempDir()
	root := filepath.Join(base, "alpha")
	require.NoError(t, smbAtomicWrite(filepath.Join(base, "owner.json"), []byte("alpha")))
	raw, err := json.Marshal(plan.Launch)
	require.NoError(t, err)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), raw))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = prepareSMBADStart(ctx, root, false)
	require.ErrorContains(t, err, "AD trust/idmap database unavailable")
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/candidate.json"), plan.Files["config/candidate.json"]))
	err = prepareSMBAccounts(context.Background(), root)
	require.ErrorContains(t, err, "AD member must not import local SAM accounts")
	_, err = os.Stat(filepath.Join(root, "samba/private/passdb.tdb"))
	require.True(t, os.IsNotExist(err))
}

func TestPrepareSMBADPathsWinbindPrivilegedDirectoryContract(t *testing.T) {
	for _, existingMode := range []os.FileMode{0, 0700, 0750} {
		t.Run(fmt.Sprintf("existing-%04o", existingMode), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "alpha")
			privileged := filepath.Join(root, "samba/state/winbindd_privileged")
			if existingMode != 0 {
				require.NoError(t, smbPrivateDir(privileged))
				require.NoError(t, os.Chmod(privileged, existingMode))
			}
			require.NoError(t, prepareSMBADPaths(root))
			for name, mode := range map[string]os.FileMode{
				"samba/winbindd":                  0755,
				"samba/state/winbindd_privileged": 0750,
				".":                               0700,
				"samba":                           0700,
				"samba/state":                     0700,
			} {
				info, err := os.Stat(filepath.Join(root, name))
				require.NoError(t, err)
				require.Equal(t, mode, info.Mode().Perm(), name)
			}
		})
	}
}

func TestPrepareSMBADPathsRejectsUnsafePrivilegedDirectory(t *testing.T) {
	for _, kind := range []string{"symlink", "file", "group-writable", "world-writable"} {
		t.Run(kind, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "alpha")
			path := filepath.Join(root, "samba/state/winbindd_privileged")
			require.NoError(t, smbPrivateDir(filepath.Dir(path)))
			switch kind {
			case "symlink":
				require.NoError(t, os.Symlink(t.TempDir(), path))
			case "file":
				require.NoError(t, os.WriteFile(path, []byte("not-a-directory"), 0600))
			case "group-writable":
				require.NoError(t, smbPrivateDir(path))
				require.NoError(t, os.Chmod(path, 0770))
			case "world-writable":
				require.NoError(t, smbPrivateDir(path))
				require.NoError(t, os.Chmod(path, 0777))
			}
			require.Error(t, prepareSMBADPaths(root))
		})
	}
}

func TestSMBADPrivatePathsPreserveState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "alpha")
	require.NoError(t, prepareSMBADPaths(root))
	for name, mode := range map[string]os.FileMode{"samba/winbindd": 0755, "samba/state/winbindd_privileged": 0750} {
		info, err := os.Stat(filepath.Join(root, name))
		require.NoError(t, err)
		require.Equal(t, mode, info.Mode().Perm())
	}
	for _, name := range []string{"samba/private/secrets.tdb", "samba/state/autorid.tdb", "ctdb/persistent/secrets.tdb.0"} {
		require.NoError(t, writeSMBNativeFile(filepath.Join(root, name), []byte("retained-database")))
	}
	require.NoError(t, prepareSMBADPaths(root))
	for _, name := range []string{"samba/private/secrets.tdb", "samba/state/autorid.tdb", "ctdb/persistent/secrets.tdb.0"} {
		raw, err := os.ReadFile(filepath.Join(root, name))
		require.NoError(t, err)
		require.Equal(t, "retained-database", string(raw))
		info, err := os.Stat(filepath.Join(root, name))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
	require.NoError(t, os.Chmod(filepath.Join(root, "samba/state/winbindd_privileged"), 0777))
	require.Error(t, prepareSMBADPaths(root), "never silently accept/reconstruct exposed privileged state")
	other := filepath.Join(t.TempDir(), "alpha")
	require.NoError(t, smbPrivateDir(filepath.Join(other, "samba")))
	require.NoError(t, os.Symlink(filepath.Join(root, "samba/winbindd"), filepath.Join(other, "samba/winbindd")))
	require.Error(t, prepareSMBADPaths(other))
}

func TestSMBADMemberIdentityBoundaries(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, tc := range []struct {
		name      string
		change    func(*smbCandidate)
		wantError string
	}{
		{"short-domain-override", func(c *smbCandidate) {
			c.Config = json.RawMessage(strings.Replace(string(c.Config), `"workgroup":"EXAMPLE"`, `"workgroup":"SHORT","server role":"m e m b e r"`, 1))
		}, ""},
		{"fifteen", func(c *smbCandidate) {
			raw, err := json.Marshal(c)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal([]byte(strings.ReplaceAll(string(raw), "alpha", "abcdefghijklmno")), c))
		}, ""},
		{"sixteen", func(c *smbCandidate) {
			raw, err := json.Marshal(c)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal([]byte(strings.ReplaceAll(string(raw), "alpha", "abcdefghijklmnop")), c))
		}, "maximum 15"},
		{"no-join", func(c *smbCandidate) { c.Joins = nil }, "ordered join"},
		{"source-count", func(c *smbCandidate) { c.Spec.JoinSources = c.Spec.JoinSources[:1] }, "ordered join"},
		{"local-users", func(c *smbCandidate) { c.Users = nativeTestCandidate(false).Users }, "local user/group"},
		{"not-clustered", func(c *smbCandidate) { c.Spec.Features = []string{"domain"} }, "CTDB mode"},
		{"idmap-range", func(c *smbCandidate) {
			c.Config = json.RawMessage(strings.Replace(string(c.Config), "2000-9999999", "3000-9999999", 1))
		}, "autorid backend and range"},
		{"keytab", func(c *smbCandidate) {
			c.Config = json.RawMessage(strings.Replace(string(c.Config), `"security":"ads"`, `"security":"ads","kerberos method":"system keytab"`, 1))
		}, "secrets-only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := nativeADCandidate()
			tc.change(&c)
			plan, err := projectSMBNative(c, "/native/"+c.Spec.ClusterID, binding)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, strings.ToUpper(c.Spec.ClusterID), plan.Launch.AD.NetBIOSName)
			require.Equal(t, "EXAMPLE.TEST", plan.Launch.AD.Realm)
			require.Equal(t, c.Spec.ClusterID+".example.test", plan.Launch.AD.FQDN)
		})
	}
	c := nativeADCandidate()
	c.Spec.CustomDNS = []string{"192.0.2.53"}
	first, err := projectSMBNative(c, "/native/alpha", binding)
	require.NoError(t, err)
	require.Equal(t, "nameserver 192.0.2.53\n", string(first.Files["config/resolv.conf"]))
	c.Joins[0] = json.RawMessage(`{"username":"new-bootstrap","password":"rotated-private"}`)
	second, err := projectSMBNative(c, "/native/alpha", binding)
	require.NoError(t, err)
	require.Equal(t, first.Files["config/smb.conf"], second.Files["config/smb.conf"])
	require.Equal(t, first.Files["config/context.json"], second.Files["config/context.json"])
}

func TestSMBADLogicalAndRuntimeModeCannotDisagree(t *testing.T) {
	t.Run("logical", func(t *testing.T) {
		candidate := nativeTestCandidate(true)
		candidate.Logical = json.RawMessage(`{"auth_mode":"active-directory","domain_settings":{"realm":"EXAMPLE.TEST"}}`)
		_, err := projectSMBNative(candidate, "/native/alpha", nativeTestBinding(t))
		require.ErrorContains(t, err, "logical authentication does not match generated spec")
	})
	t.Run("runtime", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "alpha")
		require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), []byte(`{"ad_member":{}}`)))
		_, err := loadSMBLaunch(root)
		require.ErrorContains(t, err, "AD member requires clustered runtime context")
	})
}

func TestSMBADIncompleteReadinessCannotPublishLegacySmbdHealth(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "alpha")
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), []byte(`{"clustered":true,"ctdb_pnn":0,"ad_member":{"realm":"EXAMPLE.TEST"}}`)))
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "samba/run/smbd.pid"), []byte(strconv.Itoa(os.Getpid()))))
	recorder := &smbNativeCommandRecorder{}
	old := common.ProcessExec
	common.ProcessExec = recorder
	defer func() { common.ProcessExec = old }()
	err := smbReadyNative(context.Background(), root, "smbd")
	require.Error(t, err, "a preexisting live PID/control response is not AD readiness")
	require.Empty(t, recorder.name)
	diagnostic, err := os.ReadFile(filepath.Join(root, "diagnostics/native-error"))
	require.NoError(t, err)
	require.Contains(t, string(diagnostic), "owner.json")
	require.NotContains(t, string(diagnostic), "native-ad.socket")
}

func TestSMBADMemberRejectsOverlongServiceFQDN(t *testing.T) {
	realm := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 60)
	candidate := nativeADCandidate()
	candidate.Config = json.RawMessage(strings.Replace(string(candidate.Config), "EXAMPLE.TEST", realm, 1))
	candidate.Logical = json.RawMessage(strings.Replace(string(candidate.Logical), "EXAMPLE.TEST", realm, 1))
	_, err := projectSMBNative(candidate, "/native/alpha", nativeTestBinding(t))
	require.ErrorContains(t, err, "AD service FQDN exceeds DNS length limit")
}
