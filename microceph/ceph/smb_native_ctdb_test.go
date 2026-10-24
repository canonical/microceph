package ceph

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/database"
)

const nativeHealthyCTDB = "Number of nodes:3\npnn:0 192.0.2.1 OK\npnn:2 192.0.2.3 OK (THIS NODE)\nRecovery mode:NORMAL (0)\n"

func nativeCTDBFixture(t *testing.T) (string, string) {
	t.Helper()
	useSMBNativeProcesses(t)
	common, err := os.MkdirTemp("", "smb-ctdb-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(common) })
	t.Setenv("SNAP_COMMON", common)
	base := filepath.Join(common, "smb")
	root := filepath.Join(base, "alpha")
	snap := filepath.Join(base, "snap")
	require.NoError(t, smbPrivateDir(filepath.Join(root, "ctdb/run")))
	require.NoError(t, os.MkdirAll(filepath.Join(snap, "bin"), 0700))
	require.NoError(t, smbAtomicWrite(filepath.Join(base, "owner.json"), []byte("alpha")))
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/context.json"), []byte(`{"clustered":true,"ctdb_pnn":2}`)))
	c := nativeTestCandidate(true)
	c.CTDB = &database.SMBCTDBView{SlotCount: 3, Member: database.SMBCTDBMember{Identity: "local", PNN: 2, Node: "192.0.2.3", State: "ready"}, Peers: []database.SMBCTDBMember{{Identity: "peer", PNN: 0, Node: "192.0.2.1", State: "ready"}}}
	data, err := json.Marshal(c)
	require.NoError(t, err)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/candidate.json"), data))
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "ctdb/nodes"), []byte("192.0.2.1\n#\n192.0.2.3\n")))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "mode"), []byte("healthy"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "status"), []byte(nativeHealthyCTDB), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "correct-status"), []byte(nativeHealthyCTDB), 0600))
	script := `#!/bin/sh
set -eu
mode=$(cat "$SNAP/mode")
printf '%s\n' "$*" >> "$SNAP/commands"
case "$1" in
 pnn)
  case "$mode" in
   timeout) exec sleep 10;;
   pause) touch "$SNAP/in-control"; while ! test -e "$SNAP/release-control"; do sleep 0.02; done;;
   pnn) echo 1; exit 0;;
  esac
  echo 2;;
 nodestatus) test "$2" = 2; test "$mode" != node;;
 status)
  case "$mode" in
   recovery) sed 's/NORMAL (0)/RECOVERY (1)/' "$SNAP/status";;
   membership) sed '/^pnn:0 /d' "$SNAP/status";;
   *) cat "$SNAP/status";;
  esac;;
 reloadnodes) if test -e "$SNAP/allow-reload"; then cp "$SNAP/correct-status" "$SNAP/status"; fi;;
 *) exit 9;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(snap, "bin/ctdb"), []byte(script), 0700))
	t.Setenv("SNAP", snap)
	return root, snap
}

func TestSMBMembershipReconciliationDoesNotSweepHealth(t *testing.T) {
	root, snap := nativeCTDBFixture(t)
	require.NoError(t, reconcileSMBCTDB(context.Background(), filepath.Dir(root)))
	commands, err := os.ReadFile(filepath.Join(snap, "commands"))
	require.NoError(t, err)
	require.Equal(t, "status\n", string(commands), "membership convergence needs one status query, not a full health sweep")
}

func TestSMBMembershipUpdaterDoesNotPollEveryTwoSeconds(t *testing.T) {
	_, snap := nativeCTDBFixture(t)
	require.Equal(t, 30*time.Second, smbMembershipFallbackInterval)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	runSMBMembershipUpdater(ctx, filepath.Join(os.Getenv("SNAP_COMMON"), "smb"), make(chan struct{}), nil)
	_, err := os.Stat(filepath.Join(snap, "commands"))
	require.True(t, os.IsNotExist(err), "unchanged membership must not trigger a two-second native polling loop")
}

func TestSMBMembershipUpdaterRespondsToPublicationAndFallback(t *testing.T) {
	root, snap := nativeCTDBFixture(t)
	changes := make(chan struct{}, 1)
	fallback := make(chan time.Time, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runSMBMembershipUpdater(ctx, filepath.Dir(root), changes, fallback)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("membership worker did not cancel")
		}
	}()
	observed := func(want string) bool {
		raw, err := os.ReadFile(filepath.Join(snap, "commands"))
		return err == nil && string(raw) == want
	}
	changes <- struct{}{}
	require.Eventually(t, func() bool { return observed("status\n") }, 3*time.Second, 10*time.Millisecond)
	fallback <- time.Now()
	require.Eventually(t, func() bool { return observed("status\nstatus\n") }, 3*time.Second, 10*time.Millisecond)
}

func TestSMBMembershipPublicationNotifiesOnlyCompletedChanges(t *testing.T) {
	root, _ := nativeCTDBFixture(t)
	configuration := "[database]\n"
	for _, kind := range []string{"volatile", "persistent", "state"} {
		configuration += kind + " database directory = " + root + "/ctdb/" + kind + "\n"
	}
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "ctdb/ctdb.conf"), []byte(configuration)))
	require.NoError(t, publishSMBCTDBLayout(root, false))
	raw, err := os.ReadFile(filepath.Join(root, "config/candidate.json"))
	require.NoError(t, err)
	var candidate smbCandidate
	require.NoError(t, json.Unmarshal(raw, &candidate))
	contextData, err := os.ReadFile(filepath.Join(root, "config/context.json"))
	require.NoError(t, err)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/next/config/context.json"), contextData))
	stage := func() {
		raw, err := json.Marshal(candidate)
		require.NoError(t, err)
		require.NoError(t, smbAtomicWrite(filepath.Join(root, "config/next/config/candidate.json"), raw))
	}
	oldNotify := notifySMBMembershipChangeFunc
	notifications := 0
	notifySMBMembershipChangeFunc = func() { notifications++ }
	defer func() { notifySMBMembershipChangeFunc = oldNotify }()
	stage()
	require.NoError(t, publishSMBNative(context.Background(), root, true))
	require.Zero(t, notifications, "unchanged membership must not wake a worker")
	candidate.CTDB.SlotCount = 4
	candidate.CTDB.Peers = append(candidate.CTDB.Peers, database.SMBCTDBMember{Identity: "new", PNN: 3, Node: "192.0.2.4", State: "new"})
	stage()
	require.NoError(t, publishSMBNative(context.Background(), root, true))
	require.Equal(t, 1, notifications)
	require.NoError(t, publishSMBNative(context.Background(), root, true))
	require.Equal(t, 1, notifications, "repeated publication must not retrigger membership work")
	candidate.CTDB.SlotCount = 5
	stage()
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "ctdb/ctdb.conf"), []byte(configuration+"# changed\n")))
	require.Error(t, publishSMBNative(context.Background(), root, true))
	require.Equal(t, 1, notifications, "partial/failed publication must not emit a completion hint")
}

func TestSMBGoCTDBControlRejectsUnknownOperationsBeforeExecution(t *testing.T) {
	root, snap := nativeCTDBFixture(t)
	for _, args := range [][]string{{"shell"}, {"pnn", "--help"}, {"nodestatus", "-1"}, {"nodestatus", "2", "3"}} {
		_, err := runSMBCTDB(context.Background(), root, args[0], args[1:]...)
		require.Error(t, err)
	}
	_, err := os.Stat(filepath.Join(snap, "commands"))
	require.True(t, os.IsNotExist(err))
}

func TestSMBGoCTDBStatusRejectsMalformedOrUnconfirmedMaps(t *testing.T) {
	for _, status := range []string{
		"pnn:0 192.0.2.1 OK\nRecovery mode:NORMAL (0)\n",
		strings.Replace(nativeHealthyCTDB, "nodes:3", "nodes:0", 1),
		nativeHealthyCTDB + "Number of nodes:3\n",
		strings.Replace(nativeHealthyCTDB, "pnn:2", "pnn:3", 1),
		strings.Replace(nativeHealthyCTDB, "192.0.2.3", "invalid", 1),
		strings.Replace(nativeHealthyCTDB, "NORMAL (0)", "RECOVERY (1)", 1),
		nativeHealthyCTDB + "pnn:2 192.0.2.3 OK\n",
	} {
		_, err := parseSMBCTDBStatus(status)
		require.Error(t, err)
	}
}

func TestSMBGoCTDBRetirementAndSameAddressReuseKeepSlotHistory(t *testing.T) {
	root, snap := nativeCTDBFixture(t)
	file := filepath.Join(root, "config/candidate.json")
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	var candidate smbCandidate
	require.NoError(t, json.Unmarshal(raw, &candidate))
	candidate.CTDB.SlotCount = 5
	candidate.CTDB.Peers = []database.SMBCTDBMember{{Identity: "replacement", PNN: 3, Node: "192.0.2.1", State: "new"}}
	raw, err = json.Marshal(candidate)
	require.NoError(t, err)
	require.NoError(t, writeSMBNativeFile(file, raw))
	old := "Number of nodes:5\npnn:0 192.0.2.1 DISCONNECTED\npnn:2 192.0.2.3 OK\nRecovery mode:NORMAL (0)\n"
	next := "Number of nodes:5\npnn:2 192.0.2.3 OK\npnn:3 192.0.2.1 OK\nRecovery mode:NORMAL (0)\n"
	require.NoError(t, os.WriteFile(filepath.Join(snap, "status"), []byte(old), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "correct-status"), []byte(next), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "allow-reload"), nil, 0600))
	require.NoError(t, updateSMBCTDBNodes(context.Background(), root))
	nodes, err := os.ReadFile(filepath.Join(root, "ctdb/nodes"))
	require.NoError(t, err)
	require.Equal(t, "#\n#\n192.0.2.3\n192.0.2.1\n#\n", string(nodes))
}

func TestSMBGoCTDBReloadPreservesPublishedTargetAndVerifiesResult(t *testing.T) {
	root, snap := nativeCTDBFixture(t)
	missing := strings.Replace(nativeHealthyCTDB, "pnn:0 192.0.2.1 OK\n", "", 1)
	require.NoError(t, os.WriteFile(filepath.Join(snap, "status"), []byte(missing), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ctdb/nodes"), []byte("#\n#\n192.0.2.3\n"), 0600))
	require.Error(t, updateSMBCTDBNodes(context.Background(), root), "exit zero without map convergence is not success")
	published, err := os.ReadFile(filepath.Join(root, "ctdb/nodes"))
	require.NoError(t, err)
	require.Equal(t, "192.0.2.1\n#\n192.0.2.3\n", string(published))
	commands, err := os.ReadFile(filepath.Join(snap, "commands"))
	require.NoError(t, err)
	require.Contains(t, string(commands), "reloadnodes")
	require.NoError(t, os.WriteFile(filepath.Join(snap, "allow-reload"), nil, 0600))
	require.NoError(t, updateSMBCTDBNodes(context.Background(), root))
}

func TestSMBGoCTDBReloadRejectsChangedOrUnhealthySurvivors(t *testing.T) {
	root, snap := nativeCTDBFixture(t)
	for _, line := range []string{"pnn:0 192.0.2.99 OK", "pnn:0 192.0.2.1 UNHEALTHY"} {
		status := strings.Replace(nativeHealthyCTDB, "pnn:0 192.0.2.1 OK", line, 1)
		status = strings.Replace(status, "Number of nodes:3", "Number of nodes:4", 1)
		require.NoError(t, os.WriteFile(filepath.Join(snap, "status"), []byte(status), 0600))
		require.Error(t, updateSMBCTDBNodes(context.Background(), root))
	}
	commands, err := os.ReadFile(filepath.Join(snap, "commands"))
	require.NoError(t, err)
	require.NotContains(t, string(commands), "reloadnodes")
	require.NoError(t, os.Remove(filepath.Join(root, "ctdb/nodes")))
	require.NoError(t, os.Mkdir(filepath.Join(root, "ctdb/nodes"), 0700))
	require.Error(t, updateSMBCTDBNodes(context.Background(), root))
}

func TestSMBGoCTDBReadinessUsesFreshControlsNotLegacyIPC(t *testing.T) {
	root, snap := nativeCTDBFixture(t)
	require.NoError(t, writeSMBNativeFile(filepath.Join(root, "ctdb/run/ctdb-nodes.pid"), []byte(strconv.Itoa(os.Getpid()))))
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(root, "ctdb/run/native-ready.socket"), Net: "unix"})
	require.NoError(t, err)
	stale.SetUnlinkOnClose(false)
	require.NoError(t, stale.Close())
	for _, mode := range []string{"pnn", "node", "recovery", "membership", "healthy"} {
		require.NoError(t, writeSMBNativeFile(filepath.Join(snap, "mode"), []byte(mode)))
		err := smbReadyNative(context.Background(), root, "ctdbd")
		if mode == "healthy" {
			require.NoError(t, err)
		} else {
			require.Error(t, err, mode)
		}
	}
	require.NoError(t, writeSMBNativeFile(filepath.Join(snap, "mode"), []byte("timeout")))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	require.Error(t, smbReadyNative(ctx, root, "ctdbd"))
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestSMBGoCTDBCannotAcknowledgeStopDuringControl(t *testing.T) {
	root, snap := nativeCTDBFixture(t)
	require.NoError(t, writeSMBNativeFile(filepath.Join(snap, "mode"), []byte("pause")))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- smbReadyNative(ctx, root, "ctdbd") }()
	require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(snap, "in-control")); return err == nil }, time.Second, 10*time.Millisecond)
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "stop-requested"), []byte("stop")))
	require.NoError(t, os.WriteFile(filepath.Join(snap, "release-control"), nil, 0600))
	require.Error(t, <-done)
}

func TestSMBMembershipCancellationDoesNotControlServices(t *testing.T) {
	root, snap := nativeCTDBFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, reconcileSMBCTDB(ctx, filepath.Dir(root)), context.Canceled)
	_, err := os.Stat(filepath.Join(snap, "commands"))
	require.True(t, os.IsNotExist(err))
	require.NoError(t, smbAtomicWrite(filepath.Join(root, "stop-requested"), []byte("stop")))
	require.NoError(t, reconcileSMBCTDB(context.Background(), filepath.Dir(root)))
	_, err = os.Stat(filepath.Join(snap, "commands"))
	require.True(t, os.IsNotExist(err))
}
