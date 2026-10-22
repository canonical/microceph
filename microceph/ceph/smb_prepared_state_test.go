package ceph

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func preparedADFixture(t *testing.T) (string, string) {
	t.Helper()
	root, snap := nativeCTDBFixture(t)
	launch := smbLaunch{Clustered: true, CTDBPNN: new(uint64), AD: &smbADMember{Workgroup: "EXAMPLE", Realm: "EXAMPLE.TEST"}}
	*launch.CTDBPNN = 2
	launch.Startup = smbDefaultStartup(launch)
	raw, err := json.Marshal(launch)
	require.NoError(t, err)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), raw))
	old, err := os.ReadFile(filepath.Join(snap, "bin/ctdb"))
	require.NoError(t, err)
	prefix := `#!/bin/sh
if [ "$1" = -t ]; then
 if [ "$3" = getdbmap ]; then
  echo 'name:secrets.tdb path:retained'
  echo 'name:autorid.tdb path:retained'
 elif [ "$3" = pfetch ]; then
  if [ "$4" != autorid.tdb ] || [ ! -e "$SNAP/missing-idmap" ]; then echo retained; fi
 else exit 9
 fi
 exit 0
fi
`
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/ctdb"), append([]byte(prefix), old...), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/wbinfo"), []byte("#!/bin/sh\necho \"$@\" >> \"$SNAP/wbinfo-calls\"\ncase \"$1\" in --domain-info=*) echo 'SID : S-1-5-21-1-2-3';; --sid-to-gid=*) echo 20000;; esac\n"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/getent"), []byte("#!/bin/sh\necho 'EXAMPLE\\users:x:20000:'\n"), 0700))
	return root, snap
}

func TestSMBADReadinessCannotAttestUnpreparedState(t *testing.T) {
	root, _ := preparedADFixture(t)
	require.ErrorContains(t, readySMBADRuntime(context.Background(), root), "not prepared")
	launch, err := loadSMBLaunch(root)
	require.NoError(t, err)
	require.False(t, launch.Startup["winbindd"].Prepared)
	require.False(t, launch.Startup["smbd"].Prepared)
}

func TestSMBADReadinessFencesAllocationBeforeUncertainOutcome(t *testing.T) {
	root, snap := preparedADFixture(t)
	launch, err := loadSMBLaunch(root)
	require.NoError(t, err)
	raw, err := json.Marshal(launch.AD)
	require.NoError(t, err)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "ad-initializing"), raw))
	require.NoError(t, setSMBPrepared(root, "winbindd", true))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/getent"), []byte("#!/bin/sh\nexit 1\n"), 0700))
	require.Error(t, readySMBADRuntime(context.Background(), root))
	launch, err = loadSMBLaunch(root)
	require.NoError(t, err)
	require.True(t, launch.Startup["winbindd"].Initialized)
	require.Len(t, launch.Startup["winbindd"].Records, 3)
	require.False(t, launch.Startup["smbd"].Prepared)
	require.NoError(t, os.WriteFile(filepath.Join(snap, "missing-idmap"), nil, 0600))
	require.Error(t, checkSMBADMember(context.Background(), root, true), "a stale initialization marker cannot weaken the fence")
}

func TestSMBADPreparedStateStaysStrictAcrossProjectionAndStaleSQL(t *testing.T) {
	root, _ := preparedADFixture(t)
	launch, err := loadSMBLaunch(root)
	require.NoError(t, err)
	raw, err := json.Marshal(launch.AD)
	require.NoError(t, err)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "ad-initializing"), raw))
	require.NoError(t, setSMBPrepared(root, "winbindd", true))
	require.NoError(t, readySMBADRuntime(context.Background(), root))
	launch, err = loadSMBLaunch(root)
	require.NoError(t, err)
	for _, role := range []string{"winbindd", "smbd"} {
		require.True(t, launch.Startup[role].Prepared)
		require.True(t, launch.Startup[role].Initialized)
		require.Len(t, launch.Startup[role].Records, 3)
	}
	_, err = os.Stat(filepath.Join(root, "ad-initializing"))
	require.ErrorIs(t, err, os.ErrNotExist)

	// A membership-only publication must not invalidate prepared consumers.
	next := launch
	next.Startup = smbDefaultStartup(next)
	raw, err = json.Marshal(next)
	require.NoError(t, err)
	require.NoError(t, publishSMBContext(root, raw, true))
	current, err := loadSMBLaunch(root)
	require.NoError(t, err)
	require.Equal(t, launch.Startup, current.Startup)

	// Config preparation is invalidated, but the durable allocation fence is
	// retained even if SQL completion was lost and an old marker survives.
	require.NoError(t, publishSMBContext(root, raw, false))
	current, err = loadSMBLaunch(root)
	require.NoError(t, err)
	require.False(t, current.Startup["winbindd"].Prepared)
	require.True(t, current.Startup["winbindd"].Initialized)
	marker, err := json.Marshal(current.AD)
	require.NoError(t, err)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "ad-initializing"), marker))
	require.NoError(t, prepareSMBADStart(context.Background(), root, true))
	current, err = loadSMBLaunch(root)
	require.NoError(t, err)
	require.Len(t, current.Startup["winbindd"].Records, 3)
}

func TestSMBADInventoryRefusesMissingDatabaseBeforeFetch(t *testing.T) {
	root, snap := smbADProcessFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(snap, "missing-database"), nil, 0600))
	require.ErrorContains(t, smbADRecord(context.Background(), root, "autorid.tdb", "CONFIG"), "refusing attachment")
	raw, err := os.ReadFile(filepath.Join(snap, "ctdb-calls"))
	require.NoError(t, err)
	require.Equal(t, "-t 5 getdbmap\n", string(raw))
}

func TestSMBDanglingStopIntentRemainsAStop(t *testing.T) {
	common := t.TempDir()
	t.Setenv("SNAP_COMMON", common)
	root := filepath.Join(common, "smb/alpha")
	require.NoError(t, smbPrivateDir(root))
	require.NoError(t, smbAtomicWrite(filepath.Join(common, "smb/owner.json"), []byte("alpha")))
	require.NoError(t, os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "stop-requested")))
	require.Error(t, checkSMBStartIntent(root))
	status, err := SMBLocalStatus(context.Background())
	require.NoError(t, err)
	require.True(t, status.StopRequested)
	require.False(t, status.Ready)
	require.NoError(t, reconcileSMBCTDB(context.Background(), filepath.Dir(root)), "a fenced actor must do no work")
}

func TestSMBPreparationCancellationCannotIssueServiceStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := false
	ops := smbAppOps{
		prepare: func(context.Context, string) error { cancel(); return nil },
		start:   func(string) error { started = true; return nil },
		ready:   func(context.Context, string) error { return nil },
		stop:    func(string) error { return nil },
	}
	require.Error(t, startSMBApps(ctx, t.TempDir(), []string{"smbd"}, ops))
	require.False(t, started, "cancelled preparation must not issue a new snapd action")
}

func TestSMBAccountPreparationHasStartupBudget(t *testing.T) {
	root := filepath.Join(t.TempDir(), "alpha")
	require.NoError(t, smbAtomicWrite(filepath.Join(filepath.Dir(root), "owner.json"), []byte("alpha")))
	raw, err := json.Marshal(nativeTestCandidate(false))
	require.NoError(t, err)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/candidate.json"), raw))
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), []byte(`{"clustered":false}`)))
	old := runSMBPrivateCommandFunc
	runSMBPrivateCommandFunc = func(ctx context.Context, root string, argv []string, input io.Reader) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		require.True(t, ok, "moving SAM preparation before Start must not remove its startup budget")
		require.LessOrEqual(t, time.Until(deadline), 30*time.Second)
		return []byte("{}"), nil
	}
	defer func() { runSMBPrivateCommandFunc = old }()
	require.NoError(t, prepareSMBAccounts(context.Background(), root))
}

func TestSMBUpdaterCancellationDoesNotWaitForPlacementLock(t *testing.T) {
	serviceStartMu.Lock()
	defer serviceStartMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runSMBUpdate(ctx, nil) }()
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancellation waited for the placement lock")
	}
}

func TestSMBMembershipCancellationDoesNotWaitForStopLock(t *testing.T) {
	smbMembershipMu.Lock()
	defer smbMembershipMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	base := t.TempDir()
	go func() { done <- reconcileSMBCTDB(ctx, base) }()
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancellation waited for the stop lock")
	}
}

func TestSMBCTDBLayoutKeepsDirectoryInodeAndRejectsForeignRefresh(t *testing.T) {
	root, _ := nativeCTDBFixture(t)
	configuration := "[database]\n"
	for _, kind := range []string{"volatile", "persistent", "state"} {
		configuration += kind + " database directory = " + root + "/ctdb/" + kind + "\n"
	}
	path := filepath.Join(root, "ctdb/ctdb.conf")
	require.NoError(t, smbAtomicWrite(path, []byte(configuration)))
	require.NoError(t, publishSMBCTDBLayout(root, false))
	fixed := filepath.Join(filepath.Dir(root), ".conf/ctdb")
	before, err := os.Stat(fixed)
	require.NoError(t, err)
	configuration += "\n# updated prepared configuration\n"
	require.NoError(t, smbAtomicWrite(path, []byte(configuration)))
	require.NoError(t, publishSMBCTDBLayout(root, false))
	after, err := os.Stat(fixed)
	require.NoError(t, err)
	require.True(t, os.SameFile(before, after), "snapd binds this inode")
	beforeFile, err := os.Stat(filepath.Join(fixed, "ctdb.conf"))
	require.NoError(t, err)
	require.NoError(t, publishSMBCTDBLayout(root, true))
	afterFile, err := os.Stat(filepath.Join(fixed, "ctdb.conf"))
	require.NoError(t, err)
	require.True(t, os.SameFile(beforeFile, afterFile))
	require.NoError(t, smbAtomicWrite(filepath.Join(fixed, "owner"), []byte("foreign")))
	require.Error(t, publishSMBCTDBLayout(root, true))
	retained, err := os.ReadFile(filepath.Join(fixed, "ctdb.conf"))
	require.NoError(t, err)
	require.Equal(t, configuration, string(retained))
}
