package ceph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
)

func TestSMBCoreApplyReadinessAndFailureStopOrder(t *testing.T) {
	root := t.TempDir()
	events := []string{}
	ops := smbAppOps{
		start: func(role string) error { events = append(events, "start "+role); return nil },
		ready: func(ctx context.Context, role string) error {
			events = append(events, "ready "+role)
			if role == "smbd" {
				return errors.New("not ready")
			}
			return nil
		},
		stop: func(role string) error { events = append(events, "stop "+role); return nil },
	}
	err := startSMBApps(context.Background(), root, []string{"cephfs-proxy", "smbd", "smbmetrics"}, ops)
	require.Error(t, err)
	require.NoError(t, checkSMBStartIntent(root), "a confirmed-clean failed update can retry; it is not explicit disable intent")
	require.Equal(t, []string{"start cephfs-proxy", "ready cephfs-proxy", "start smbd", "ready smbd", "stop smbmetrics", "stop smbd", "stop winbindd", "stop cephfs-proxy", "stop ctdbd"}, events)
	_, err = os.Stat(filepath.Join(root, "applied.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestSMBStopIntentSurvivesFailedStop(t *testing.T) {
	root := t.TempDir()
	ops := smbAppOps{stop: func(role string) error {
		if role == "smbd" {
			return errors.New("uncertain stop")
		}
		return nil
	}}
	require.Error(t, stopSMBApps(root, ops))
	b, err := os.ReadFile(filepath.Join(root, "stop-requested"))
	require.NoError(t, err)
	require.NotEmpty(t, b)
	require.Error(t, checkSMBStartIntent(root), "updater/startup must not resurrect a failed disable")
}

func TestSMBFixedAppPlanAndPayload(t *testing.T) {
	roles, err := smbCoreRoles(smbSpec{ClusterID: "alpha"}, false)
	require.NoError(t, err)
	require.Equal(t, []string{"smbd", "smbmetrics"}, roles)
	roles, err = smbCoreRoles(smbSpec{ClusterID: "alpha", Features: []string{"cephfs-proxy"}}, true)
	require.NoError(t, err)
	require.Equal(t, []string{"cephfs-proxy", "smbd", "smbmetrics"}, roles)
	var p SMBServicePlacement
	require.NoError(t, p.PopulateParams(nil, `{"cluster_id":"alpha"}`))
	require.Error(t, p.PopulateParams(nil, `{"cluster_id":"../alpha"}`))
}

func TestSMBMemberRemovalKeepsGroupIdentity(t *testing.T) {
	var got string
	old := deleteSMBServiceFunc
	deleteSMBServiceFunc = func(ctx context.Context, c mcTypes.Client, target string, svc *types.SMBService) error {
		got = target + "/" + svc.ClusterID
		return nil
	}
	defer func() { deleteSMBServiceFunc = old }()
	require.NoError(t, deleteGroupedSMBService(context.Background(), nil, types.Service{Service: "smb", Location: "member", GroupID: "alpha"}))
	require.Equal(t, "member/alpha", got)
	require.Error(t, deleteGroupedSMBService(context.Background(), nil, types.Service{Service: "smb", Location: "member"}))
}

func TestSMBAtomicWriteRejectsEscapingTemporaryFile(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(outside, []byte("preserve"), 0600))
	path := filepath.Join(root, "config.json")
	require.NoError(t, os.Symlink(outside, path+".tmp"))
	require.Error(t, smbAtomicWrite(path, []byte("secret")))
	content, err := os.ReadFile(outside)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(content))
}
