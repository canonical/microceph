package ceph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/constants"
)

func TestCanonicalCephConfigScopedSMBRecoveryKeyring(t *testing.T) {
	dir := t.TempDir()
	oldPaths := constants.GetPathConst
	constants.GetPathConst = func() constants.PathConst { return constants.PathConst{ConfPath: dir} }
	t.Cleanup(func() { constants.GetPathConst = oldPaths })
	keyring := "/var/snap/microceph/common/smb/alpha/identity/recovery.keyring"
	data := map[string]any{
		"fsid": "test-fsid", "monitors": "192.0.2.1", "isCache": true,
		"smbRecoveryClients": []map[string]string{{"Name": "client.smb.cluster.alpha", "Keyring": keyring}},
	}
	config := NewCephConfig(constants.CephConfFileName)
	for _, monitor := range []string{"192.0.2.1", "192.0.2.2"} {
		data["monitors"] = monitor
		require.NoError(t, config.WriteConfig(data, 0644))
		raw, err := os.ReadFile(filepath.Join(dir, "ceph.conf"))
		require.NoError(t, err)
		text := string(raw)
		require.Contains(t, text, "mon host = "+monitor)
		require.Contains(t, text, "rbd_cache = true")
		require.Contains(t, text, "[client.smb.cluster.alpha]\nkeyring = "+keyring+"\n")
		require.Equal(t, 1, strings.Count(text, "[client.smb.cluster.alpha]"))
		require.NotContains(t, strings.Split(text, "[client]")[0], "keyring =", "never change the global keyring default")
	}
	delete(data, "smbRecoveryClients")
	require.NoError(t, config.WriteConfig(data, 0644))
	raw, err := os.ReadFile(config.GetPath())
	require.NoError(t, err)
	require.NotContains(t, string(raw), "client.smb.cluster.", "no relevant owner means no stale stanza")
}

func TestSMBNativeCTDBUsesCanonicalCephConfig(t *testing.T) {
	raw, err := os.ReadFile("../../snapcraft/commands/smb-startup")
	require.NoError(t, err)
	require.NotContains(t, string(raw), "export CEPH_CONF", "CTDB uses the canonical /etc/ceph directory layout")
	require.NotContains(t, string(raw), "CEPH_ARGS")
	require.NotContains(t, string(raw), "--bind /etc/ceph/ceph.conf", "canonical config must follow atomic replacement, not a stale file bind")
}
