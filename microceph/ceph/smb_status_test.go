package ceph

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSMBLocalStatusAfterReservationPublicationFailure(t *testing.T) {
	state, runner := smbLocalStartupFixture(t, "")
	require.NoError(t, smbAtomicWrite(filepath.Join(smbRoot("alpha"), "diagnostics/policy-warning/obstacle"), []byte("retain")))
	require.Error(t, enableSMBLocked(context.Background(), state, "alpha"))
	_, err := os.Stat(filepath.Join(smbBase(), "owner.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
	var before string
	require.NoError(t, runner.db.QueryRow("SELECT config FROM service_groups").Scan(&before))
	runner.commands = nil
	status, err := SMBLocalStatus(context.Background(), state)
	require.NoError(t, err)
	require.Equal(t, "alpha", status.ClusterID)
	require.Contains(t, status.Error, "uncertain")
	require.False(t, status.Ready)
	require.Empty(t, runner.commands)
	var after string
	require.NoError(t, runner.db.QueryRow("SELECT config FROM service_groups").Scan(&after))
	require.Equal(t, before, after)
	_, err = os.Stat(filepath.Join(smbBase(), "owner.json"))
	require.ErrorIs(t, err, os.ErrNotExist, "status must not repair missing owner publication")
}

func TestSMBLocalStatusFreshReadyAndStopped(t *testing.T) {
	state, runner := smbLocalStartupFixture(t, "")
	status, err := SMBLocalStatus(context.Background(), state)
	require.NoError(t, err)
	require.Empty(t, status.ClusterID)
	require.NoError(t, enableSMBLocked(context.Background(), state, "alpha"))
	status, err = SMBLocalStatus(context.Background(), state)
	require.NoError(t, err)
	require.True(t, status.Ready)
	require.Equal(t, "alpha", status.ClusterID)
	require.NoError(t, stopSMBApps(smbRoot("alpha"), smbSnapOps(smbRoot("alpha"))))
	runner.commands = nil
	status, err = SMBLocalStatus(context.Background(), state)
	require.NoError(t, err)
	require.True(t, status.StopRequested)
	require.False(t, status.Ready)
	require.Empty(t, runner.commands)
	require.NoError(t, DisableSMB(context.Background(), state, "alpha", false))
	status, err = SMBLocalStatus(context.Background(), state)
	require.NoError(t, err)
	require.Empty(t, status.ClusterID)
}
