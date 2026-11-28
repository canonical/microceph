package ceph

import (
	"context"
	"database/sql"
	"errors"
	"os"

	"encoding/json"
	"github.com/canonical/microceph/microceph/interfaces"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/canonical/microceph/microceph/database"

	"github.com/stretchr/testify/require"
)

func TestSMBClusteredAdmissionAndRoleBoundary(t *testing.T) {
	spec, err := decodeSMBSpec("alpha", []byte(`{"service_type":"smb","service_id":"alpha","spec":{"cluster_id":"alpha","features":["clustered"],"cluster_meta_uri":"rados://.smb/alpha/cluster.meta","cluster_lock_uri":"rados://.smb/alpha/cluster.lock","config_uri":"rados://.smb/alpha/config.smb"}}`))
	require.NoError(t, err)
	require.NoError(t, validateSMBLogicalPolicy([]byte(`{"auth_mode":"user","clustering":"always"}`), spec))
	require.NoError(t, validateSMBEffectivePolicy(spec, smbBindingDescription{Clustered: true}))
	roles, err := smbCoreRoles(spec)
	require.NoError(t, err)
	require.Equal(t, []string{"ctdbd", "smbd"}, roles)
	require.Error(t, validateSMBLogicalPolicy([]byte(`{"auth_mode":"user","clustering":"never"}`), spec))
	spec.Features = append(spec.Features, "domain")
	_, err = smbCoreRoles(spec)
	require.Error(t, err)
}

func clusteredTestCandidate() smbCandidate {
	c := nativeTestCandidate()
	c.Spec.Features = []string{"clustered"}
	c.Spec.ClusterMetaURI = "rados://.smb/alpha/cluster.meta"
	c.Spec.ClusterLockURI = "rados://.smb/alpha/cluster.lock"
	c.Config = json.RawMessage(strings.Replace(string(c.Config), `"instance_name":"alpha"`, `"instance_name":"alpha","instance_features":["ctdb"]`, 1))
	c.CTDB = &database.SMBCTDBView{SlotCount: 3, Member: database.SMBCTDBMember{Identity: "1", PNN: 0, Node: "192.0.2.1", State: "new"}, Peers: []database.SMBCTDBMember{{Identity: "3", PNN: 2, Node: "192.0.2.3", State: "new"}}}
	c.Spec.ConfigURI = "rados://.smb/alpha/config.smb"
	c.Spec.UserSources = []string{"rados://.smb/alpha/users"}
	c.SpecBytes, _ = json.Marshal(map[string]any{"service_type": "smb", "service_id": "alpha", "spec": c.Spec})
	c.Logical = json.RawMessage(`{"resource_type":"ceph.smb.cluster","cluster_id":"alpha","auth_mode":"user","clustering":"always"}`)
	return c
}

func TestSMBClusteredProjectionPreservesSlotsAndRecoveryIdentity(t *testing.T) {
	c := clusteredTestCandidate()
	root := filepath.Join(t.TempDir(), "alpha")
	plan, err := projectSMBNative(c, root, nativeTestBinding(t))
	require.NoError(t, err)
	require.True(t, plan.Launch.Clustered)
	require.Equal(t, "192.0.2.1\n#\n192.0.2.3\n", string(plan.Files["ctdb/nodes"]))
	require.Contains(t, string(plan.Files["ctdb/ctdb.conf"]), "client.smb.cluster.alpha .smb cluster.lock -n alpha -R")
	require.Contains(t, string(plan.Files["config/smb.conf"]), "ctdb:registry.tdb = yes")
	old := c.configurationFingerprint()
	c.CTDB.SlotCount++
	require.Equal(t, old, c.configurationFingerprint(), "membership-only changes must not restart SAM/serving roles")
}

func TestSMBClusteredEnableNoOpMembershipAndPeerSafePurge(t *testing.T) {
	state, r := smbLocalStartupFixture(t, "")
	r.clustered = true
	r.logical = `{"resource_type":"ceph.smb.cluster","cluster_id":"alpha","auth_mode":"user","clustering":"always"}`
	r.spec = strings.Replace(r.spec, `"spec":{`, `"spec":{"features":["clustered"],"cluster_meta_uri":"rados://.smb/alpha/cluster.meta","cluster_lock_uri":"rados://.smb/alpha/cluster.lock",`, 1)
	r.config = strings.Replace(r.config, `"alpha":{`, `"alpha":{"instance_features":["ctdb"],`, 1)
	oldReady, oldPrepare, oldUpdate := smbReadyNativeFunc, prepareSMBCTDBFilesFunc, updateConfigFunc
	t.Cleanup(func() {
		smbReadyNativeFunc, prepareSMBCTDBFilesFunc, updateConfigFunc = oldReady, oldPrepare, oldUpdate
	})
	smbReadyNativeFunc = func(ctx context.Context, root, role string) error {
		if role == "ctdbd" && r.ctdbActive {
			return nil
		}
		if role == "smbd" && r.active && r.ctdbActive {
			return nil
		}
		return errors.New("not ready")
	}
	prepareSMBCTDBFilesFunc = func(context.Context, string, string) error { return nil }
	updateConfigFunc = func(context.Context, interfaces.StateInterface) error { return nil }
	ctx := context.Background()
	require.NoError(t, enableSMBLocked(ctx, state, "alpha"))
	require.True(t, r.active)
	require.True(t, r.ctdbActive)
	root := smbRoot("alpha")
	_, err := os.Stat(filepath.Join(root, "identity/local-sam"))
	require.ErrorIs(t, err, os.ErrNotExist)
	r.commands = nil
	require.NoError(t, enableSMBLocked(ctx, state, "alpha"))
	for _, cmd := range r.commands {
		for _, effect := range []string{" accounts ", " render ", "snapctl restart", "snapctl stop", "snapctl start"} {
			require.NotContains(t, cmd, effect)
		}
	}
	r.spec = strings.Replace(r.spec, `"bind_addrs":`, `"custom_ports":{"smb":2445},"bind_addrs":`, 1)
	r.commands = nil
	require.NoError(t, refreshSMBLocked(ctx, state))
	require.Contains(t, strings.Join(r.commands, "\n"), "snapctl restart microceph.smbd")
	for _, cmd := range r.commands {
		require.NotContains(t, cmd, "stop microceph.ctdbd")
		require.NotContains(t, cmd, "restart microceph.ctdbd")
	}
	_, err = r.db.Exec(`INSERT INTO core_cluster_members VALUES(2,'b','192.0.2.2:7443')`)
	require.NoError(t, err)
	require.NoError(t, state.ClusterState().Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := database.ReserveSMBClusteredOwner(ctx, tx, "alpha", "b")
		return err
	}))
	r.commands = nil
	require.NoError(t, refreshSMBLocked(ctx, state))
	assertNoRefreshEffects(t, r)
	require.NoError(t, reconcileSMBCTDB(ctx, filepath.Dir(root)))
	nodes, err := os.ReadFile(filepath.Join(root, "ctdb/nodes"))
	require.NoError(t, err)
	require.Equal(t, "192.0.2.1\n192.0.2.2\n", string(nodes))
	oldRemove := removeSMBLocalStateFunc
	t.Cleanup(func() { removeSMBLocalStateFunc = oldRemove })
	removeSMBLocalStateFunc = func(string) error { return errors.New("injected local destruction failure") }
	require.Error(t, DisableSMB(ctx, state, "alpha", true))
	removeSMBLocalStateFunc = oldRemove
	require.NoError(t, DisableSMB(ctx, state, "alpha", true), "exact retired clustered history must permit safe purge retry")
	var raw string
	require.NoError(t, r.db.QueryRow("SELECT config FROM service_groups").Scan(&raw))
	require.Contains(t, raw, `"owners":{"2":"b"}`)
	require.Contains(t, raw, `"ranks":{"1":0,"2":1}`)
}

func TestSMBClusteredPrivateControlUsesIsolatedNamespace(t *testing.T) {
	root, snap := localNativeFixture(t)
	cmd, err := newSMBPrivateCommand(context.Background(), root, []string{filepath.Join(snap, "bin/ctdb"), "status"})
	require.NoError(t, err)
	require.Equal(t, uintptr(syscall.CLONE_NEWNS), cmd.SysProcAttr.Cloneflags)
	require.Contains(t, cmd.Args[2], "smb_setup_ctdb")
}
