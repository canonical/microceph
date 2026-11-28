package ceph

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSMBADCompleteAdmission(t *testing.T) {
	c := clusteredTestCandidate()
	c.Spec.Features = append(c.Spec.Features, "domain")
	c.Spec.JoinSources = []string{"rados:mon-config-key:smb/config/alpha/join.0.json"}
	c.Logical = json.RawMessage(`{"auth_mode":"active-directory","clustering":"always","domain_settings":{"realm":"EXAMPLE.TEST"}}`)
	c.Users = nil
	c.Spec.UserSources = nil
	c.Config = json.RawMessage(strings.Replace(string(c.Config), `"server string":"literal {{.value}}"`, `"security":"ads","realm":"EXAMPLE.TEST","workgroup":"EXAMPLE","idmap config * : backend":"autorid","idmap config * : range":"2000-9999999"`, 1))
	require.NoError(t, validateSMBLogicalPolicy(c.Logical, c.Spec), "complete clustered AD must be admitted")
	roles, err := smbCoreRoles(c.Spec)
	require.NoError(t, err)
	require.Equal(t, []string{"ctdbd", "winbindd", "smbd"}, roles)
}

func TestSMBADStartupRequiresCompleteIdentityAndMonotonicFence(t *testing.T) {
	raw := []byte(`{"clustered":true,"ctdb_pnn":0,"ad_member":{"realm":"EXAMPLE.TEST","workgroup":"EXAMPLE","netbios_name":"ALPHA","fqdn":"alpha.example.test"},"startup":{"winbindd":{"prepared":true,"initialized":true},"smbd":{"prepared":true,"initialized":true}}}`)
	launch, err := decodeSMBLaunch(raw)
	require.NoError(t, err, "AD startup must consume admitted clustered identity")
	root := filepath.Join(t.TempDir(), "alpha")
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), raw))
	require.NoError(t, publishSMBContext(root, raw))
	updated, err := loadSMBLaunchForPublication(root)
	require.NoError(t, err)
	require.False(t, updated.Startup["winbindd"].Prepared, "publication invalidates consumers")
	var before, after map[string]any
	encoded, err := json.Marshal(launch)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(encoded, &before))
	encoded, err = json.Marshal(updated)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(encoded, &after))
	require.Equal(t, true, after["startup"].(map[string]any)["winbindd"].(map[string]any)["initialized"], "new projection must never relax allocation fence")
}

func TestSMBADBootstrapFingerprintExcludesOnlyJoinInputs(t *testing.T) {
	c := nativeADCandidate()
	c.SpecBytes, _ = json.Marshal(map[string]any{"spec": c.Spec})
	c.Logical = json.RawMessage(`{"auth_mode":"active-directory","domain_settings":{"realm":"EXAMPLE.TEST","join_sources":[{"ref":"first"}]}}`)
	before, full := c.configurationFingerprint(), c.fingerprint()
	c.Joins[0] = json.RawMessage(`{"username":"changed","password":"changed-private"}`)
	c.Spec.JoinSources[0] = "rados:mon-config-key:smb/config/alpha/changed"
	c.SpecBytes, _ = json.Marshal(map[string]any{"spec": c.Spec})
	c.Logical = json.RawMessage(strings.Replace(string(c.Logical), "first", "changed", 1))
	require.Equal(t, before, c.configurationFingerprint(), "bootstrap-only refresh must not restart serving")
	require.NotEqual(t, full, c.fingerprint(), "source recheck includes ordered join inputs")
	c.SpecBytes = json.RawMessage(strings.Replace(string(c.SpecBytes), `"spec":{`, `"unrelated":"changed","spec":{`, 1))
	require.NotEqual(t, before, c.configurationFingerprint(), "unrelated generated metadata remains a serving input")
}

func TestSMBADPrivateDispatcherAdmitsOnlyExactNativeOperations(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "alpha")
	require.NoError(t, smbAtomicWrite(filepath.Join(base, "owner.json"), []byte("alpha")))
	snap := t.TempDir()
	t.Setenv("SNAP", snap)
	for _, argv := range [][]string{
		{"ctdb", "-t", "5", "getdbmap"},
		{"ctdb", "-t", "5", "pfetch", "autorid.tdb", "0x434f4e46494700"},
		{"ctdb", "-t", "5", "pfetch", "secrets.tdb", "SECRETS/SID/EXAMPLE"},
		{"wbinfo", "--ping"}, {"wbinfo", "--check-secret"}, {"wbinfo", "--domain-info=EXAMPLE"},
		{"wbinfo", "--sid-to-gid=S-1-5-21-1-2-3-513"}, {"getent", "group", "2000"},
	} {
		argv[0] = filepath.Join(snap, "bin", argv[0])
		_, err := newSMBPrivateCommand(context.Background(), root, argv)
		require.NoError(t, err, argv)
	}
	for _, argv := range [][]string{
		{"ctdb", "-t", "5", "pfetch", "unowned.tdb", "key"}, {"ctdb", "-t", "5", "wipe"},
		{"ctdb", "-t", "6", "getdbmap"}, {"ctdb", "-t", "5", "pfetch", "autorid.tdb", "CONFIG"},
		{"wbinfo", "--all-domains"}, {"wbinfo", "--ping", "--check-secret"},
		{"wbinfo", "--domain-info=EXAMPLE\nINJECT"}, {"wbinfo", "--sid-to-gid=S-1-5-21-1-2-3-512"},
		{"getent", "passwd", "2000"}, {"getent", "group", "-1"}, {"net", "ads", "join"},
	} {
		argv[0] = filepath.Join(snap, "bin", argv[0])
		_, err := newSMBPrivateCommand(context.Background(), root, argv)
		require.Error(t, err, argv)
	}
}

func TestSMBADReadinessCannotPrepareSmbdBeforeSQLInitialization(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "alpha")
	require.NoError(t, smbAtomicWrite(filepath.Join(base, "owner.json"), []byte("alpha")))
	member := &smbADMember{Realm: "EXAMPLE.TEST", Workgroup: "EXAMPLE", NetBIOSName: "ALPHA", FQDN: "alpha.example.test"}
	launch := smbLaunch{Clustered: true, CTDBPNN: new(uint64), AD: member}
	launch.Startup = smbDefaultStartup(launch)
	raw, err := json.Marshal(launch)
	require.NoError(t, err)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), raw))
	require.NoError(t, setSMBPrepared(root, "winbindd", true))
	require.NoError(t, advanceSMBADStartup(root, false))
	require.NoError(t, advanceSMBADStartup(root, true))
	updated, err := loadSMBLaunch(root)
	require.NoError(t, err)
	require.False(t, updated.Startup["smbd"].Prepared, "native readiness does not authorize serving before the distinct SQL commit")
}
