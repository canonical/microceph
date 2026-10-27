package ceph

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// nativeTestBinding exercises the real hash-pinned upstream adapter.
func nativeTestBinding(t *testing.T) smbBinding {
	t.Helper()
	python := os.Getenv("MICROCEPH_SMB_TEST_PYTHON")
	if python == "" {
		t.Skip("set MICROCEPH_SMB_TEST_PYTHON and pinned Sambacc PYTHONPATH")
	}
	script, err := filepath.Abs("../../snapcraft/smb/native.py")
	require.NoError(t, err)
	return func(command string, request any, result any) error {
		raw, err := json.Marshal(request)
		if err != nil {
			return err
		}
		code := `import importlib.util,json,sys
spec=importlib.util.spec_from_file_location("binding",sys.argv[1]);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
print(json.dumps(m.bridge(sys.argv[2],json.load(sys.stdin))))`
		cmd := exec.Command(python, "-B", "-c", code, script, command)
		cmd.Stdin = strings.NewReader(string(raw))
		output, err := cmd.Output()
		if err != nil {
			return err
		}
		return json.Unmarshal(output, result)
	}
}

func nativeTestCandidate() smbCandidate {
	c := smbCandidate{Spec: smbSpec{ClusterID: "alpha"}, CephConfig: "[global]\nmon host = 192.0.2.1\n[client.admin]\nkeyring = /admin\n", Endpoint: &smbEndpoint{FrontendAddress: "2001:db8::7", SMBPort: 1445}}
	c.Config = json.RawMessage(`{"samba-container-config":"v0","configs":{"alpha":{"instance_name":"alpha","globals":["default"],"shares":["data"]}},"globals":{"default":{"options":{"server string":"literal {{.value}}"}}},"shares":{"data":{"options":{"path":"/data","vfs objects":"acl_xattr ceph_new","ceph_new:proxy":"no","ceph_new:filesystem":"fs","ceph_new:config_file":"/etc/ceph/ceph.conf","ceph_new:user_id":"smb.fs.cluster.alpha","read only":"yes","browseable":"no","comment":"Archive","max connections":"2"}}}}`)
	c.Users = []json.RawMessage{json.RawMessage(`{"users":{"all_entries":[{"name":"alice","password":"secret","uid":0,"gid":3000},{"name":"bob","password":"other"}]},"groups":{"all_entries":[{"name":"team","gid":3000}]}}`)}
	return c
}

func TestSMBNativeProjectionUsesUpstreamRenderingWithoutPublication(t *testing.T) {
	c := nativeTestCandidate()
	root := filepath.Join(t.TempDir(), "alpha")
	before, err := json.Marshal(c)
	require.NoError(t, err)
	plan, err := projectSMBNative(c, root, nativeTestBinding(t))
	require.NoError(t, err)
	after, err := json.Marshal(c)
	require.NoError(t, err)
	require.Equal(t, before, after)
	_, err = os.Stat(root)
	require.True(t, os.IsNotExist(err), "projection must not create directories")
	conf := string(plan.Files["config/smb.conf"])
	for _, value := range []string{"literal {{.value}}", "read only = yes", "browseable = no", "comment = Archive", "max connections = 2", "interfaces = 2001:db8::7", "smb ports = 1445", root + "/config/ceph-direct.conf"} {
		require.Contains(t, conf, value)
	}
	require.NotContains(t, conf, "secret")
	require.Contains(t, string(plan.Files["config/passwd"]), "alice:x:0:3000:")
	require.Contains(t, string(plan.Files["config/passwd"]), "bob:x:1001:1001:")
	require.Contains(t, string(plan.Files["config/group"]), "team:x:3000:")
	require.Equal(t, "passwd: files\ngroup: files\nhosts: files dns\nservices: files\n", string(plan.Files["config/nsswitch.conf"]))
	require.NotContains(t, string(plan.Files["config/ceph-direct.conf"]), "client.admin")
	require.Contains(t, string(plan.Files["config/ceph-direct.conf"]), root+"/identity/data.keyring")
	require.Equal(t, []smbDataCheck{{Filesystem: "fs", Path: "/data", User: "smb.fs.cluster.alpha"}}, plan.Launch.DataChecks)
	require.Equal(t, map[string]smbStartupChecks{"smbd": {}}, plan.Launch.Startup)
}

func TestSMBNativeProjectionPreservesUpstreamOptionOrder(t *testing.T) {
	candidate := nativeTestCandidate()
	candidate.Config = json.RawMessage(strings.Replace(string(candidate.Config), `"server string":"literal {{.value}}"`, `"server string":"ordered server","workgroup":"LOCAL"`, 1))
	plan, err := projectSMBNative(candidate, "/native/alpha", nativeTestBinding(t))
	require.NoError(t, err)
	conf := string(plan.Files["config/smb.conf"])
	for _, text := range []string{"server string = ordered server", "workgroup = LOCAL", "comment = Archive", "ceph_new:config_file ="} {
		require.Contains(t, conf, text)
	}
	require.Less(t, strings.Index(conf, "server string = ordered server"), strings.Index(conf, "workgroup = LOCAL"))
	require.Less(t, strings.Index(conf, "comment = Archive"), strings.Index(conf, "ceph_new:config_file ="))
}

func TestSMBNativeProjectionRejectsNativeOverrideAndIdentityEscapes(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, test := range []struct{ name, from, to string }{
		{"global override", `"server string":"literal {{.value}}"`, `"PRIVATE  DIR":"/outside"`},
		{"provider alias", `"ceph_new:config_file":"/etc/ceph/ceph.conf"`, `"ceph_new:config_file":"/etc/ceph/ceph.conf","CEPH_NEW:CONFIG_FILE":"/outside"`},
		{"proxy", `"ceph_new:proxy":"no"`, `"ceph_new:proxy":"yes"`},
		{"proxy type", `"ceph_new:proxy":"no"`, `"ceph_new:proxy":true`},
		{"VFS type", `"vfs objects":"acl_xattr ceph_new"`, `"vfs objects":7`},
		{"data identity", "smb.fs.cluster.alpha", "admin"},
		{"effective AD", `"server string":"literal {{.value}}"`, `"security":" AdS "`},
		{"effective CTDB", `"server string":"literal {{.value}}"`, `"clustering":"yes"`},
		{"controller", `"server string":"literal {{.value}}"`, `"domain logons":"yes"`},
		{"NSS newline", `"alice"`, `"ali\nce"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := nativeTestCandidate()
			c.Config = json.RawMessage(strings.Replace(string(c.Config), test.from, test.to, 1))
			c.Users[0] = json.RawMessage(strings.Replace(string(c.Users[0]), test.from, test.to, 1))
			_, err := projectSMBNative(c, "/native/alpha", binding)
			require.Error(t, err)
		})
	}
	c := nativeTestCandidate()
	c.Users = append(c.Users, c.Users[0])
	_, err := projectSMBNative(c, "/native/alpha", binding)
	require.Error(t, err)
}

func TestSMBNativePublicationFencesPreparationBeforeCandidate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "alpha")
	require.NoError(t, writeSMBNativeFile(filepath.Join(root, "config/context.json"), []byte(`{"startup":{"smbd":{"prepared":true}}}`)))
	require.NoError(t, writeSMBNativeFile(filepath.Join(root, "config/next/config/context.json"), []byte(`{"startup":{"smbd":{"prepared":true}}}`)))
	require.NoError(t, writeSMBNativeFile(filepath.Join(root, "config/next/config/candidate.json"), []byte(`{"config":{"version":"new"}}`)))
	require.NoError(t, writeSMBNativeFile(filepath.Join(root, "config/next/config/ceph-direct.conf"), []byte("candidate")))
	obstacle := filepath.Join(root, "config/ceph-direct.conf")
	require.NoError(t, os.Mkdir(obstacle, 0700))
	require.Error(t, publishSMBNative(context.Background(), root))
	advanced, err := os.ReadFile(filepath.Join(root, "config/candidate.json"))
	require.NoError(t, err)
	require.JSONEq(t, `{"config":{"version":"new"}}`, string(advanced), "failure occurs after candidate advancement")
	for _, retry := range []bool{false, true} {
		if retry {
			require.NoError(t, os.Remove(obstacle))
			require.NoError(t, publishSMBNative(context.Background(), root))
		}
		launch, err := loadSMBLaunchForPublication(root)
		require.NoError(t, err)
		require.False(t, launch.Startup["smbd"].Prepared)
	}
}

func TestSMBPublicationRejectsIncompleteContextBeforeCandidate(t *testing.T) {
	for _, raw := range []string{"", "{", `{"clustered":true}`, `{"startup":{"winbindd":{}}}`} {
		root := filepath.Join(t.TempDir(), "alpha")
		path := filepath.Join(root, "config/candidate.json")
		contextRaw := []byte(`{"startup":{"smbd":{"prepared":true}}}`)
		require.NoError(t, writeSMBNativeFile(filepath.Join(root, "config/context.json"), contextRaw))
		require.NoError(t, writeSMBNativeFile(path, []byte(`{"version":"old"}`)))
		require.NoError(t, writeSMBNativeFile(filepath.Join(root, "config/next/config/candidate.json"), []byte(`{"version":"new"}`)))
		if raw != "" {
			require.NoError(t, writeSMBNativeFile(filepath.Join(root, "config/next/config/context.json"), []byte(raw)))
		}
		require.Error(t, publishSMBNative(context.Background(), root))
		retained, err := os.ReadFile(path)
		require.NoError(t, err)
		require.JSONEq(t, `{"version":"old"}`, string(retained))
		retained, err = os.ReadFile(filepath.Join(root, "config/context.json"))
		require.NoError(t, err)
		require.Equal(t, contextRaw, retained, "invalid projection must not change the live context")
	}
}

func TestSMBNativeCompilerStagesInGoBeforePublication(t *testing.T) {
	useSMBNativeProcesses(t)
	python := os.Getenv("MICROCEPH_SMB_TEST_PYTHON")
	if python == "" {
		t.Skip("set MICROCEPH_SMB_TEST_PYTHON and pinned Sambacc PYTHONPATH")
	}
	base := t.TempDir()
	root, snap := filepath.Join(base, "alpha"), filepath.Join(base, "snap")
	require.NoError(t, smbPrivateDir(filepath.Join(root, "config")))
	require.NoError(t, os.MkdirAll(filepath.Join(snap, "bin"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(snap, "smb"), 0700))
	script, err := filepath.Abs("../../snapcraft/smb/native.py")
	require.NoError(t, err)
	require.NoError(t, os.Symlink(script, filepath.Join(snap, "smb/native.py")))
	require.NoError(t, os.Symlink(python, filepath.Join(snap, "bin/python3")))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/testparm"), []byte("#!/bin/sh\ntest \"$1\" = --suppress-prompt && test -s \"$2\"\n"), 0700))
	t.Setenv("SNAP", snap)
	candidate := nativeTestCandidate()
	candidate.Config = json.RawMessage(`{"samba-container-config":"v0","configs":{"alpha":{"globals":[],"shares":[]}},"globals":{},"shares":{}}`)
	live := filepath.Join(root, "config/smb.conf")
	require.NoError(t, os.WriteFile(live, []byte("retained"), 0600))
	require.NoError(t, writeSMBNativeFile(filepath.Join(root, "config/next/config/obsolete"), []byte("obsolete")))
	require.NoError(t, compileSMBNative(context.Background(), root, candidate))
	before, err := os.ReadFile(live)
	require.NoError(t, err)
	require.Equal(t, "retained", string(before), "compile must not publish live files")
	_, err = os.Stat(filepath.Join(root, "config/next/config/obsolete"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, publishSMBNative(context.Background(), root))
	published, err := os.ReadFile(live)
	require.NoError(t, err)
	require.Contains(t, string(published), "smb ports = 1445")
	files, err := filepath.Glob(filepath.Join(root, "config/.native-input-*"))
	require.NoError(t, err)
	require.Empty(t, files, "private bridge requests must be removed")
	for _, path := range []string{live, filepath.Join(root, "config/context.json"), filepath.Join(root, "config/next/config/smb.conf")} {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
}

func TestSMBNativePublicationRejectsSymlinksBeforeLiveWrites(t *testing.T) {
	for _, location := range []string{"staged", "live", "ancestor"} {
		t.Run(location, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "alpha")
			live := filepath.Join(root, "config/smb.conf")
			require.NoError(t, writeSMBNativeFile(live, []byte("retained")))
			require.NoError(t, writeSMBNativeFile(filepath.Join(root, "config/next/config/context.json"), []byte(`{"startup":{"smbd":{}}}`)))
			staged := filepath.Join(root, "config/next/config/smb.conf")
			outside := filepath.Join(base, "outside")
			require.NoError(t, os.WriteFile(outside, []byte("outside"), 0600))
			if location == "staged" {
				require.NoError(t, os.Symlink(outside, staged))
			} else {
				require.NoError(t, writeSMBNativeFile(staged, []byte("candidate")))
				if location == "live" {
					require.NoError(t, os.Remove(live))
					require.NoError(t, os.Symlink(outside, live))
				} else {
					require.NoError(t, os.Rename(root, root+"-retained"))
					require.NoError(t, os.Symlink(root+"-retained", root))
				}
			}
			require.Error(t, publishSMBNative(context.Background(), root))
			retained, err := os.ReadFile(live)
			require.NoError(t, err)
			if location == "live" {
				require.Equal(t, "outside", string(retained))
			} else {
				require.Equal(t, "retained", string(retained))
			}
			retained, err = os.ReadFile(outside)
			require.NoError(t, err)
			require.Equal(t, "outside", string(retained))
		})
	}
}

func TestSMBDirectOperationsRejectInvalidRootBeforeIO(t *testing.T) {
	for _, op := range []func(string) error{
		func(root string) error { return compileSMBNative(context.Background(), root, nativeTestCandidate()) },
		func(root string) error { return publishSMBNative(context.Background(), root) },
		func(root string) error { return smbReadyNative(context.Background(), root, "smbd") },
	} {
		base := t.TempDir()
		require.EqualError(t, op(base+"/alpha/../alpha"), "invalid native cluster root")
		entries, err := os.ReadDir(base)
		require.NoError(t, err)
		require.Empty(t, entries)
	}
}

func TestSMBDirectFileOperationsHonorCancellation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "alpha")
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/next/config/smb.conf"), []byte("candidate")))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, publishSMBNative(ctx, root))
	diagnostic, err := os.ReadFile(filepath.Join(root, "diagnostics/native-error"))
	require.NoError(t, err)
	require.Contains(t, string(diagnostic), "context canceled")
	_, err = os.Stat(filepath.Join(root, "config/smb.conf"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
