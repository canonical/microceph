package ceph

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A second deferred certificate update first activates and verifies B, then
// publishes C with B as the durable rollback target, even across another retry.
func TestRGWDeferredRecoveryRebasesNextJournal(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	a, b, c := testRGWSpec(t), testRGWSpec(t), testRGWSpec(t)
	deferRGWRotation(t, rec, a, b)
	bConf := readTestConf(t)

	rb, err := applyRGWFrontend(c, []string{"mon1"}, false)
	require.NoError(t, err)
	require.NotNil(t, rb)
	assert.Equal(t, 1, rec.restarts, "recovery activates B even when C is deferred")
	assert.Equal(t, 2, rec.readyChecks)
	assert.Contains(t, readTestConf(t), c.tlsGenDir())
	marker, err := readPendingApply()
	require.NoError(t, err)
	require.NotNil(t, marker)
	require.NotNil(t, marker.PreviousConfig)
	assert.Equal(t, bConf, *marker.PreviousConfig)
	require.NotNil(t, marker.PreviousActive)
	assert.True(t, *marker.PreviousActive)
	assert.DirExists(t, a.tlsGenDir(), "rebase must not prune any generations")
	assert.DirExists(t, b.tlsGenDir())
	assert.DirExists(t, c.tlsGenDir())

	// A new invocation must load the durable marker from disk and recover C;
	// the old A baseline must never reappear as the rollback target.
	rb, err = applyRGWFrontend(c, []string{"mon1"}, false)
	require.NoError(t, err)
	assert.Nil(t, rb)
	assert.Equal(t, 2, rec.restarts)
	assert.Equal(t, 3, rec.readyChecks)
	assert.NoFileExists(t, rgwPendingApplyPath())
	assert.NoDirExists(t, a.tlsGenDir())
	assert.NoDirExists(t, b.tlsGenDir())
	assert.DirExists(t, c.tlsGenDir())
}

// A retry of deferred C reloads the rebased B journal from disk. If recovery
// fails, verified rollback restores B rather than the original A.
func TestRGWDeferredRecoveryFailedNextActivationRestoresB(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	a, b, c := testRGWSpec(t), testRGWSpec(t), testRGWSpec(t)
	deferRGWRotation(t, rec, a, b)
	bConf := readTestConf(t)
	_, err := applyRGWFrontend(c, []string{"mon1"}, false)
	require.NoError(t, err)
	rec.readyFailures = map[int]error{3: errRGWInactive}
	_, err = applyTestRGWFrontend(c, []string{"mon1"}, true)
	require.Error(t, err)
	assert.Equal(t, bConf, readTestConf(t))
	assert.Equal(t, 3, rec.restarts, "recover B, attempt recovery of C, restore B")
	assert.Equal(t, 4, rec.readyChecks, "restoration must be verified")
	assert.NoFileExists(t, rgwPendingApplyPath())
	assert.NoDirExists(t, a.tlsGenDir())
	assert.DirExists(t, b.tlsGenDir())
	assert.NoDirExists(t, c.tlsGenDir())
}

// An invalid current config is restored, not repaired using the new request.
func TestRGWRecoveryRejectsInvalidCurrentConfiguration(t *testing.T) {
	for _, frontend := range []string{"", "rgw frontends = beast port=not-a-port", "rgw frontends = beast", "rgw frontends = beast port=0", "rgw frontends = beast ssl_port=443 ssl_certificate=/missing/server.crt ssl_private_key=/missing/server.key"} {
		t.Run(frontend, func(t *testing.T) {
			defer setupRGWPaths(t)()
			rec := &rgwOpsRecorder{}
			defer rec.install(t)()
			a, b, c := testRGWSpec(t), testRGWSpec(t), testRGWSpec(t)
			aConf := deferRGWRotation(t, rec, a, b)
			before := readTestConf(t)
			invalid := strings.Replace(before, rgwFrontendLine([]byte(before)), frontend, 1)
			require.NotEqual(t, before, invalid)
			require.NoError(t, writeRGWFile(rgwConfPath(), []byte(invalid), 0644))
			_, err := applyRGWFrontend(c, []string{"mon1"}, false)
			require.Error(t, err)
			assert.Equal(t, aConf, readTestConf(t))
			assert.NoFileExists(t, rgwPendingApplyPath())
			assert.Equal(t, 1, rec.restarts, "only the rollback frontend is activated")
			assert.DirExists(t, a.tlsGenDir())
			assert.NoDirExists(t, b.tlsGenDir())
			assert.NoDirExists(t, c.tlsGenDir())
		})
	}
}

// A failed recovery whose rollback restart also fails is indeterminate. Do
// not remove the journal or any TLS generations in that state.
func TestRGWRecoveryFailedRollbackKeepsJournalAndMaterial(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{restartErrOn: 2, restartErr: errors.New("rollback restart failed")}
	defer rec.install(t)()
	a, b, c := testRGWSpec(t), testRGWSpec(t), testRGWSpec(t)
	deferRGWRotation(t, rec, a, b)
	marker, err := os.ReadFile(rgwPendingApplyPath())
	require.NoError(t, err)
	rec.readyFailures = map[int]error{2: errRGWInactive}
	_, err = applyRGWFrontend(c, []string{"mon1"}, false)
	require.Error(t, err)
	assert.ErrorContains(t, err, "rollback restart failed")
	retained, err := os.ReadFile(rgwPendingApplyPath())
	require.NoError(t, err)
	assert.Equal(t, marker, retained)
	assert.Equal(t, 2, rec.restarts)
	assert.DirExists(t, a.tlsGenDir())
	assert.DirExists(t, b.tlsGenDir())
	assert.NoDirExists(t, c.tlsGenDir())
}

// A journal from before the probe_safe field existed remains usable, including
// the previous inactive state which recovery must preserve on failed startup.
func TestRGWLegacyInactiveJournalRecoveryFailure(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	b := testRGWSpec(t)
	_, err := applyRGWFrontend(b, []string{"mon1"}, false)
	require.NoError(t, err)
	legacy, err := json.Marshal(map[string]any{"previous_config": "", "previously_active": false})
	require.NoError(t, err)
	require.NoError(t, writeRGWFile(rgwPendingApplyPath(), legacy, 0600))
	rec.readyFailures = map[int]error{1: errRGWInactive}
	_, err = applyRGWFrontend(testRGWSpec(t), []string{"mon1"}, false)
	require.Error(t, err)
	assert.NoFileExists(t, rgwConfPath(), "rollback restores the original inactive state")
	assert.Equal(t, 1, rec.starts)
	assert.Equal(t, 1, rec.stops)
	assert.NoDirExists(t, b.tlsGenDir())
	assert.NoFileExists(t, rgwPendingApplyPath())
}

func TestRGWRecoveryUnconfirmedRollbackRetainsGenerations(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	a, b, c := testRGWSpec(t), testRGWSpec(t), testRGWSpec(t)
	deferRGWRotation(t, rec, a, b)
	marker, err := os.ReadFile(rgwPendingApplyPath())
	require.NoError(t, err)
	rec.readyFailures = map[int]error{
		2: errors.New("B not ready"),
		3: errors.New("A restoration not ready"),
	}
	_, err = applyRGWFrontend(c, []string{"mon1"}, false)
	require.ErrorContains(t, err, "B not ready")
	assert.ErrorContains(t, err, "A restoration not ready")
	retained, err := os.ReadFile(rgwPendingApplyPath())
	require.NoError(t, err)
	assert.Equal(t, marker, retained)
	assert.DirExists(t, a.tlsGenDir())
	assert.DirExists(t, b.tlsGenDir())
	assert.NoDirExists(t, c.tlsGenDir())
}

func TestRGWRecoveryInactiveRollbackRequiresStoppedService(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	b, c := testRGWSpec(t), testRGWSpec(t)
	_, err := applyRGWFrontend(b, []string{"mon1"}, false)
	require.NoError(t, err)
	checkRGWReadyFunc = func(rgwFrontendSpec) error { return errors.New("listener unavailable") }
	stopRGWFunc = func() error { return nil } // A successful command alone is not proof.
	_, err = applyRGWFrontend(c, []string{"mon1"}, false)
	require.ErrorContains(t, err, "did not become inactive")
	assert.FileExists(t, rgwPendingApplyPath())
	assert.DirExists(t, b.tlsGenDir())
	assert.NoDirExists(t, c.tlsGenDir())
}

func TestRGWRecoveryDoesNotNeedPreviousCertificate(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	a, b := testRGWSpec(t), testRGWSpec(t)
	deferRGWRotation(t, rec, a, b)
	require.NoError(t, os.Remove(filepath.Join(a.tlsGenDir(), "server.crt")))
	_, err := applyTestRGWFrontend(b, []string{"mon1"}, true)
	require.NoError(t, err)
	assert.Equal(t, 1, rec.restarts)
	assert.Contains(t, readTestConf(t), b.tlsGenDir())
	assert.NoFileExists(t, rgwPendingApplyPath())
}

func TestRGWRecoveryRejectsMalformedJournal(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	a, b, c := testRGWSpec(t), testRGWSpec(t), testRGWSpec(t)
	deferRGWRotation(t, rec, a, b)
	before := readTestConf(t)
	malformed := []byte(`{"previous_config": null}`)
	require.NoError(t, writeRGWFile(rgwPendingApplyPath(), malformed, 0600))
	_, err := applyRGWFrontend(c, []string{"mon1"}, false)
	require.Error(t, err)
	assert.Equal(t, before, readTestConf(t))
	retained, err := os.ReadFile(rgwPendingApplyPath())
	require.NoError(t, err)
	assert.Equal(t, malformed, retained)
	assert.Zero(t, rec.restarts)
	assert.DirExists(t, a.tlsGenDir())
	assert.DirExists(t, b.tlsGenDir())
	assert.NoDirExists(t, c.tlsGenDir())
}

func TestRGWRecoveryBeforeFirstConfigPublication(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	require.NoError(t, writePendingApplyMarker(nil, false))
	c := testRGWSpec(t)
	_, err := applyRGWFrontend(c, []string{"mon1"}, true)
	require.Error(t, err, "restore the unconfigured state, do not apply the request")
	assert.NoFileExists(t, rgwConfPath())
	assert.NoFileExists(t, rgwPendingApplyPath())
	assert.NoDirExists(t, c.tlsGenDir())
	assert.Zero(t, rec.starts)
	assert.Equal(t, 1, rec.stops)
}

func TestRGWRecoveryRebaseFailureStopsNextPublication(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	a, b, c := testRGWSpec(t), testRGWSpec(t), testRGWSpec(t)
	deferRGWRotation(t, rec, a, b)
	bConf := readTestConf(t)
	checkRGWReadyFunc = func(rgwFrontendSpec) error {
		// Force the atomic marker replacement to fail after B is activated.
		err := os.Remove(rgwPendingApplyPath())
		if err != nil {
			return err
		}
		return os.Mkdir(rgwPendingApplyPath(), 0700)
	}
	_, err := applyRGWFrontend(c, []string{"mon1"}, false)
	require.Error(t, err)
	assert.Equal(t, bConf, readTestConf(t))
	assert.Equal(t, 1, rec.restarts)
	assert.DirExists(t, rgwPendingApplyPath())
	assert.DirExists(t, a.tlsGenDir())
	assert.DirExists(t, b.tlsGenDir())
	assert.NoDirExists(t, c.tlsGenDir())
}

func TestRGWRecoveryAllowsMonitorRefreshDuringReadiness(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	a, b, c := testRGWSpec(t), testRGWSpec(t), testRGWSpec(t)
	deferRGWRotation(t, rec, a, b)
	checkRGWReadyFunc = func(spec rgwFrontendSpec) error {
		if !radosgwConfMu.TryLock() {
			return errors.New("recovery held the config lock during readiness")
		}
		defer radosgwConfMu.Unlock()
		conf, err := readRGWConf()
		if err != nil {
			return err
		}
		assert.Equal(t, b.certPEM, spec.certPEM, "readiness checks B, not the incoming C")
		return writeRGWFile(rgwConfPath(), []byte(strings.ReplaceAll(string(conf), "mon host = mon1", "mon host = mon2")), 0644)
	}
	_, err := applyRGWFrontend(c, []string{"mon1"}, false)
	require.NoError(t, err)
	assert.Contains(t, readTestConf(t), "mon host = mon2")
	pending, err := readPendingApply()
	require.NoError(t, err)
	require.NotNil(t, pending)
	assert.Contains(t, *pending.PreviousConfig, "mon host = mon2")
	assert.Contains(t, *pending.PreviousConfig, b.tlsGenDir())
}
