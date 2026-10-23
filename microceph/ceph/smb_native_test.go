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

	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

type smbNativeCommandRecorder struct {
	name string
	args []string
}

func (r *smbNativeCommandRecorder) RunCommand(name string, args ...string) (string, error) {
	return r.RunCommandContext(context.Background(), name, args...)
}

func (r *smbNativeCommandRecorder) RunCommandContext(_ context.Context, name string, args ...string) (string, error) {
	r.name, r.args = name, args
	return "", nil
}

// nativeTestBinding executes only the real upstream adapter, with no file or
// command mocks inside it. Native projection itself must not publish anything.
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

func nativeTestCandidate(clustered bool) smbCandidate {
	features := `[]`
	c := smbCandidate{Spec: smbSpec{ClusterID: "alpha", ClusterMetaURI: "rados://.smb/alpha/cluster.meta", ClusterLockURI: "rados://.smb/alpha/cluster.lock"}, CephConfig: "[global]\nmon host = 192.0.2.1\n[client.admin]\nkeyring = /admin\n", Endpoint: &smbEndpoint{FrontendAddress: "2001:db8::7", SMBPort: 1445, MetricsPort: 19922}}
	if clustered {
		features = `["ctdb"]`
		c.Spec.Features = []string{"clustered"}
		c.CTDB = &database.SMBCTDBView{SlotCount: 5, Member: database.SMBCTDBMember{Identity: "host-c", PNN: 3, Node: "192.0.2.3", State: "ready"}, Peers: []database.SMBCTDBMember{{Identity: "host-a", PNN: 0, Node: "192.0.2.1", State: "new"}, {Identity: "retired", PNN: 2, Node: "192.0.2.2", State: "gone"}}}
	}
	c.Config = json.RawMessage(`{"samba-container-config":"v0","configs":{"alpha":{"instance_name":"alpha","instance_features":` + features + `,"globals":["default"],"shares":["data"]}},"globals":{"default":{"options":{"server string":"literal {{.value}}"}}},"shares":{"data":{"options":{"path":"/data","vfs objects":"acl_xattr ceph_new","ceph_new:proxy":"yes","ceph_new:filesystem":"fs","ceph_new:config_file":"/etc/ceph/ceph.conf","ceph_new:user_id":"smb.fs.cluster.alpha","read only":"yes","browseable":"no","comment":"Archive","max connections":"2"}}}}`)
	c.Users = []json.RawMessage{json.RawMessage(`{"users":{"all_entries":[{"name":"alice","password":"secret","uid":0,"gid":3000},{"name":"bob","password":"other"}]},"groups":{"all_entries":[{"name":"team","gid":3000}]}}`)}
	return c
}

func TestSMBNativeProjectionUsesUpstreamRenderingWithoutPublication(t *testing.T) {
	binding := nativeTestBinding(t)
	for _, clustered := range []bool{false, true} {
		c := nativeTestCandidate(clustered)
		root := filepath.Join(t.TempDir(), "alpha")
		before, err := json.Marshal(c)
		require.NoError(t, err)
		plan, err := projectSMBNative(c, root, binding)
		require.NoError(t, err)
		after, err := json.Marshal(c)
		require.NoError(t, err)
		require.Equal(t, before, after)
		_, err = os.Stat(root)
		require.True(t, os.IsNotExist(err), "projection must not publish or create directories")
		require.Equal(t, clustered, plan.Launch.Clustered)
		require.NotContains(t, plan.Files, "config/ctdb-recovery.conf", "CTDB uses the generated canonical client section")
		require.True(t, plan.Launch.Proxy)
		conf := string(plan.Files["config/smb.conf"])
		for _, value := range []string{"literal {{.value}}", "read only = yes", "browseable = no", "comment = Archive", "max connections = 2", "interfaces = 2001:db8::7", "smb ports = 1445", root + "/config/ceph-proxy.conf"} {
			require.Contains(t, conf, value)
		}
		require.NotContains(t, conf, "secret")
		require.Contains(t, string(plan.Files["config/passwd"]), "alice:x:0:3000:")
		require.Contains(t, string(plan.Files["config/passwd"]), "bob:x:1001:1001:")
		require.Contains(t, string(plan.Files["config/group"]), "team:x:3000:")
		require.NotContains(t, string(plan.Files["config/ceph-direct.conf"]), "client.admin")
		require.Contains(t, string(plan.Files["config/ceph-direct.conf"]), root+"/identity/data.keyring")
		if clustered {
			require.Equal(t, "192.0.2.1\n#\n#\n192.0.2.3\n#\n", string(plan.Files["ctdb/nodes"]))
			require.Contains(t, conf, "ctdb:registry.tdb = yes")
			require.Contains(t, conf, "registry shares = no")
			require.NotContains(t, conf, "include = registry")
			require.Contains(t, string(plan.Files["ctdb/ctdb.conf"]), "client.smb.cluster.alpha .smb cluster.lock -n alpha -R")
			require.NotContains(t, string(plan.Files["ctdb/ctdb.conf"]), "script directory", "pinned CTDB ignores this unsupported option")
			require.Contains(t, string(plan.Files["config/ctdb-rank"]), "3\n")
		}
	}
}

func TestSMBNativeProjectionPreservesUpstreamOptionOrder(t *testing.T) {
	candidate := nativeTestCandidate(false)
	candidate.Config = json.RawMessage(strings.Replace(string(candidate.Config), `"server string":"literal {{.value}}"`, `"server string":"before include","include":"/fixture/upstream.conf"`, 1))
	plan, err := projectSMBNative(candidate, "/native/alpha", nativeTestBinding(t))
	require.NoError(t, err)
	conf := string(plan.Files["config/smb.conf"])
	for _, text := range []string{"server string = before include", "include = /fixture/upstream.conf", "comment = Archive", "ceph_new:config_file ="} {
		require.Contains(t, conf, text)
	}
	require.Less(t, strings.Index(conf, "server string = before include"), strings.Index(conf, "include = /fixture/upstream.conf"), "Go JSON serialization must not reorder upstream options around includes")
	require.Less(t, strings.Index(conf, "comment = Archive"), strings.Index(conf, "ceph_new:config_file ="), "controlled share options remain after upstream options, as in the former adapter")
}

func TestSMBNativeProjectionRejectsNativeOverrideAndIdentityEscapes(t *testing.T) {
	binding := nativeTestBinding(t)
	cases := []struct {
		name   string
		change func(*smbCandidate)
	}{
		{"global override", func(c *smbCandidate) {
			c.Config = json.RawMessage(strings.Replace(string(c.Config), `"server string":"literal {{.value}}"`, `"PRIVATE  DIR":"/outside"`, 1))
		}},
		{"provider alias", func(c *smbCandidate) {
			c.Config = json.RawMessage(strings.Replace(string(c.Config), `"ceph_new:config_file":"/etc/ceph/ceph.conf"`, `"ceph_new:config_file":"/etc/ceph/ceph.conf","CEPH_NEW:CONFIG_FILE":"/outside"`, 1))
		}},
		{"proxy option type", func(c *smbCandidate) {
			c.Config = json.RawMessage(strings.Replace(string(c.Config), `"ceph_new:proxy":"yes"`, `"ceph_new:proxy":true`, 1))
		}},
		{"VFS option type", func(c *smbCandidate) {
			c.Config = json.RawMessage(strings.Replace(string(c.Config), `"vfs objects":"acl_xattr ceph_new"`, `"vfs objects":7`, 1))
		}},
		{"data identity", func(c *smbCandidate) {
			c.Config = json.RawMessage(strings.Replace(string(c.Config), "smb.fs.cluster.alpha", "admin", 1))
		}},
		{"duplicate users", func(c *smbCandidate) { c.Users = append(c.Users, c.Users[0]) }},
		{"NSS newline", func(c *smbCandidate) {
			c.Users[0] = json.RawMessage(strings.Replace(string(c.Users[0]), `"alice"`, `"ali\nce"`, 1))
		}},
		{"public address", func(c *smbCandidate) {
			c.Config = json.RawMessage(strings.Replace(string(c.Config), `"shares":{`, `"ctdb":{"public_addresses":[{"address":"192.0.2.99"}]},"shares":{`, 1))
		}},
		{"domain mode", func(c *smbCandidate) {
			c.Config = json.RawMessage(strings.Replace(string(c.Config), `["ctdb"]`, `["ctdb","addc"]`, 1))
		}},
		{"domain security", func(c *smbCandidate) {
			c.Config = json.RawMessage(strings.Replace(string(c.Config), `"server string":"literal {{.value}}"`, `"security":" AdS "`, 1))
		}},
		{"mode mismatch", func(c *smbCandidate) { c.Spec.Features = nil }},
		{"registry include", func(c *smbCandidate) {
			c.Config = json.RawMessage(strings.Replace(string(c.Config), `"server string":"literal {{.value}}"`, `"include":"registry"`, 1))
		}},
		{"legacy nodes alias", func(c *smbCandidate) {
			c.Config = json.RawMessage(strings.Replace(string(c.Config), `"shares":{`, `"ctdb":{"nodes_json":"/outside"},"shares":{`, 1))
		}},
		{"arbitrary node command", func(c *smbCandidate) {
			c.Config = json.RawMessage(strings.Replace(string(c.Config), `"shares":{`, `"ctdb":{"nodes_cmd":"arbitrary-command"},"shares":{`, 1))
		}},
		{"rank collision", func(c *smbCandidate) { c.CTDB.Member.PNN = 0 }},
		{"duplicate address", func(c *smbCandidate) { c.CTDB.Member.Node = "192.0.2.1" }},
		{"invalid address", func(c *smbCandidate) { c.CTDB.Member.Node = "fe80::1%eth0" }},
		{"inactive member", func(c *smbCandidate) { c.CTDB.Member.State = "gone" }},
		{"rank bound", func(c *smbCandidate) { c.CTDB.Member.PNN = 65536 }},
		{"slot bound", func(c *smbCandidate) { c.CTDB.SlotCount = 2 }},
		{"foreign lock", func(c *smbCandidate) { c.Spec.ClusterLockURI = "rados://.smb/other/cluster.lock" }},
		{"lock argument", func(c *smbCandidate) { c.Spec.ClusterLockURI = "rados://.smb/alpha/-R" }},
		{"same objects", func(c *smbCandidate) { c.Spec.ClusterLockURI = c.Spec.ClusterMetaURI }},
		{"different pools", func(c *smbCandidate) { c.Spec.ClusterLockURI = "rados://other/alpha/cluster.lock" }},
		{"URI query", func(c *smbCandidate) { c.Spec.ClusterLockURI = "rados://.smb/alpha/cluster.lock?x=y" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := nativeTestCandidate(true)
			tc.change(&c)
			_, err := projectSMBNative(c, "/native/alpha", binding)
			require.Error(t, err)
		})
	}
}

func TestSMBNativeCephConfigPreservesProtectedRendering(t *testing.T) {
	data, err := smbNativeCephConfig("[global]\nmon host = 192.0.2.1\nrun dir = /run/ceph\n[client.admin]\nkeyring = /admin\n", "alpha", "/native/alpha")
	require.NoError(t, err)
	require.Equal(t, "[global]\nmon host = 192.0.2.1\nrun dir = /run/ceph\nkeyring = /native/alpha/identity/data.keyring\n\n[client.smb.fs.cluster.alpha]\nkeyring = /native/alpha/identity/data.keyring\n\n", string(data))
}

func TestSMBNativeRefreshRetainsEquivalentCTDBDescriptorBytes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "alpha")
	require.NoError(t, smbPrivateDir(filepath.Join(root, "config/next/config")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "config/next/config/context.json"), []byte(`{"clustered":false}`), 0600))
	old := []byte("{\"ctdb\": {\"ctdb_port\": 0}, \"samba-container-config\": \"v0\"}\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "config/ctdb.json"), old, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "config/next/config/ctdb.json"), []byte(`{"samba-container-config":"v0","ctdb":{"ctdb_port":0}}`), 0600))
	require.NoError(t, publishSMBNative(context.Background(), root, true))
	retained, err := os.ReadFile(filepath.Join(root, "config/ctdb.json"))
	require.NoError(t, err)
	require.Equal(t, old, retained, "format-only differences must not rewrite protected runtime files")
	require.NoError(t, os.WriteFile(filepath.Join(root, "config/next/config/ctdb.json"), []byte(`{"samba-container-config":"v0","ctdb":{"ctdb_port":4379}}`), 0600))
	require.Error(t, publishSMBNative(context.Background(), root, true), "semantic CTDB changes still require maintenance")
}

func TestSMBNativeCompilerStagesInGoBeforePublication(t *testing.T) {
	useSMBNativeProcesses(t)
	python := os.Getenv("MICROCEPH_SMB_TEST_PYTHON")
	if python == "" {
		t.Skip("set MICROCEPH_SMB_TEST_PYTHON and pinned Sambacc PYTHONPATH")
	}
	base := t.TempDir()
	root := filepath.Join(base, "alpha")
	snap := filepath.Join(base, "snap")
	require.NoError(t, smbPrivateDir(filepath.Join(root, "config")))
	require.NoError(t, os.MkdirAll(filepath.Join(snap, "bin"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(snap, "smb"), 0700))
	script, err := filepath.Abs("../../snapcraft/smb/native.py")
	require.NoError(t, err)
	require.NoError(t, os.Symlink(script, filepath.Join(snap, "smb/native.py")))
	require.NoError(t, os.Symlink(python, filepath.Join(snap, "bin/python3")))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/testparm"), []byte("#!/bin/sh\ntest \"$1\" = --suppress-prompt && test -s \"$2\"\n"), 0700))
	t.Setenv("SNAP", snap)
	candidate := nativeTestCandidate(false)
	candidate.Config = json.RawMessage(`{"samba-container-config":"v0","configs":{"alpha":{"globals":[],"shares":[]}},"globals":{},"shares":{}}`)
	live := filepath.Join(root, "config/smb.conf")
	require.NoError(t, os.WriteFile(live, []byte("retained"), 0600))
	require.NoError(t, compileSMBNative(context.Background(), root, candidate))
	before, err := os.ReadFile(live)
	require.NoError(t, err)
	require.Equal(t, "retained", string(before), "compile must not publish live files")
	require.NoError(t, publishSMBNative(context.Background(), root, false))
	published, err := os.ReadFile(live)
	require.NoError(t, err)
	require.Contains(t, string(published), "smb ports = 1445")
	files, err := filepath.Glob(filepath.Join(root, "config/.native-input-*"))
	require.NoError(t, err)
	require.Empty(t, files, "private bridge requests must be removed")
}

func TestSMBNativePublicationRetainsCTDBAndStopsBeforeUnsafeWrites(t *testing.T) {
	root := filepath.Join(t.TempDir(), "alpha")
	require.NoError(t, smbPrivateDir(filepath.Join(root, "config/next/config")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "config/next/config/smb.conf"), []byte("[global]\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "config/next/config/context.json"), []byte(`{"clustered":false}`), 0600))
	require.NoError(t, publishSMBNative(context.Background(), root, false))
	content, err := os.ReadFile(filepath.Join(root, "config/smb.conf"))
	require.NoError(t, err)
	require.Equal(t, "[global]\n", string(content))
	require.NoError(t, smbPrivateDir(filepath.Join(root, "config/next/ctdb")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "config/next/ctdb/ctdb.conf"), []byte("changed"), 0600))
	require.Error(t, publishSMBNative(context.Background(), root, true), "refresh must not replace CTDB startup configuration")
	require.NoError(t, os.Remove(filepath.Join(root, "config/next/ctdb/ctdb.conf")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "stop-requested"), []byte("stop"), 0600))
	require.Error(t, publishSMBNative(context.Background(), root, true))
}

func TestSMBDirectCompilerKeepsBindingFailuresPrivate(t *testing.T) {
	// Exercise ServiceInit's real preparation boundary, not a replacement helper
	// CLI or compiler mock. Only the external binding returns a private failure.
	var err error
	runSMBStartup(t, "binding", false, "unused-binding-interpreter", func(_ interfaces.StateInterface, r *smbStartupRunner) {
		err = r.startupErr
	})
	root := smbRoot("alpha")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "source-password-private")
	require.Contains(t, err.Error(), "inspect private cluster diagnostics")
	diagnostic, readErr := os.ReadFile(filepath.Join(root, "diagnostics/native-error"))
	require.NoError(t, readErr)
	require.Contains(t, string(diagnostic), "source-password-private")
	info, statErr := os.Stat(filepath.Join(root, "diagnostics/native-error"))
	require.NoError(t, statErr)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestSMBDirectOperationsRejectInvalidRootBeforeIO(t *testing.T) {
	operations := map[string]func(string) error{
		"compile": func(root string) error {
			return compileSMBNative(context.Background(), root, nativeTestCandidate(false))
		},
		"publish":      func(root string) error { return publishSMBNative(context.Background(), root, false) },
		"prepare-ctdb": func(root string) error { return prepareSMBCTDBFiles(context.Background(), root, "/missing-snap") },
		"ready":        func(root string) error { return smbReadyNative(context.Background(), root, "smbd") },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			require.EqualError(t, operation(base+"/alpha/../alpha"), "invalid native cluster root")
			entries, err := os.ReadDir(base)
			require.NoError(t, err)
			require.Empty(t, entries, "invalid roots must not even receive diagnostics")
		})
	}
}

func TestSMBDirectFileOperationsHonorCancellation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "alpha")
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/next/config/smb.conf"), []byte("candidate")))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// The old subprocess boundary rejected cancellation before doing any work.
	// Direct operations must preserve that cancellation boundary.
	operations := []func(context.Context) error{
		func(ctx context.Context) error { return publishSMBNative(ctx, root, false) },
		func(ctx context.Context) error { return prepareSMBCTDBFiles(ctx, root, "/missing-snap") },
	}
	for _, operation := range operations {
		require.Error(t, operation(ctx))
		diagnostic, err := os.ReadFile(filepath.Join(root, "diagnostics/native-error"))
		require.NoError(t, err)
		require.Contains(t, string(diagnostic), "context canceled")
	}
	_, err := os.Stat(filepath.Join(root, "config/smb.conf"))
	require.ErrorIs(t, err, os.ErrNotExist, "canceled work must not publish live config")
}
