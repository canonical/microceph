package ceph

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rgwOpsRecorder captures calls to the injectable RGW primitives so tests can
// assert start vs restart vs stop and keyring creation without a running snap
// or Ceph cluster.
//
// active models the pre-change liveness read; readyErr, when set, is returned
// by every active check after the first one, i.e. the post-action readiness
// gate. serves is the certificate the running gateway presents: the marker
// probe succeeds only against it, and probes counts the attempts.
type rgwOpsRecorder struct {
	starts   int
	restarts int
	stops    int
	keyrings int
	probes   int

	active   bool
	readyErr error
	serves   []byte

	checkAfterAction bool

	startErr   error
	stopErr    error
	restartErr error
	// restartErrOn fails the Nth restart call (1-based, 0 = none), simulating
	// a restart failure on a specific transition.
	restartErrOn int
}

// install swaps the injectable primitives for recorder-backed fakes and
// returns a restore closure. getConfigDbFunc is redirected to a static
// monitor list so pipeline tests need no dqlite.
func (r *rgwOpsRecorder) install(t *testing.T) func() {
	t.Helper()
	origStart, origRestart, origStop := startRGWFunc, restartRGWFunc, stopRGWFunc
	origCheck, origKey, origCfg := checkRGWActiveFunc, createRGWKeyringFunc, getConfigDbFunc
	origReady, origFrontend, origInterval := checkRGWReadyFunc, checkRGWFrontendFunc, rgwPendingProbeInterval
	checkRGWReadyFunc = func(rgwFrontendSpec) error { return checkRGWActiveFunc() }
	checkRGWFrontendFunc = func(spec rgwFrontendSpec) error {
		r.probes++
		if r.serves != nil && bytes.Equal(spec.certPEM, r.serves) {
			return nil
		}
		return errors.New("the gateway does not serve the probed certificate")
	}
	rgwPendingProbeInterval = 0

	startRGWFunc = func() error {
		r.starts++
		if r.startErr == nil {
			r.active = true
			r.checkAfterAction = true
		}
		return r.startErr
	}
	restartRGWFunc = func() error {
		r.restarts++
		if r.restartErrOn == r.restarts {
			r.active = false
			return r.restartErr
		}
		r.active = true
		r.checkAfterAction = true
		return nil
	}
	stopRGWFunc = func() error {
		r.stops++
		if r.stopErr == nil {
			r.active = false
			r.checkAfterAction = false
		}
		return r.stopErr
	}
	checkRGWActiveFunc = func() error {
		if r.checkAfterAction {
			r.checkAfterAction = false
			err := r.readyErr
			r.readyErr = nil
			if err != nil {
				r.active = false
				return err
			}
		}
		if r.active {
			return nil
		}
		return errRGWInactive
	}
	createRGWKeyringFunc = func(string) error { r.keyrings++; return nil }
	getConfigDbFunc = func(context.Context, interfaces.StateInterface) (map[string]string, error) {
		return map[string]string{"mon.host.a": "10.0.0.1"}, nil
	}
	return func() {
		startRGWFunc, restartRGWFunc, stopRGWFunc = origStart, origRestart, origStop
		checkRGWActiveFunc, createRGWKeyringFunc, getConfigDbFunc = origCheck, origKey, origCfg
		checkRGWReadyFunc, checkRGWFrontendFunc, rgwPendingProbeInterval = origReady, origFrontend, origInterval
	}
}

// setupRGWPaths overrides constants.GetPathConst to point at temp directories
// so the RGW lifecycle writes radosgw.conf / TLS generations in isolation.
// Returns a restore closure.
func setupRGWPaths(t *testing.T) (restore func()) {
	t.Helper()
	tmp := t.TempDir()
	confPath := filepath.Join(tmp, "conf")
	runPath := filepath.Join(tmp, "run")
	dataPath := filepath.Join(tmp, "data")
	sslPath := filepath.Join(tmp, "ssl")
	for _, d := range []string{confPath, runPath, dataPath, sslPath} {
		require.NoError(t, os.MkdirAll(d, 0755))
	}
	orig := constants.GetPathConst
	constants.GetPathConst = func() constants.PathConst {
		return constants.PathConst{
			ConfPath:     confPath,
			RunPath:      runPath,
			DataPath:     dataPath,
			SSLFilesPath: sslPath,
		}
	}
	return func() { constants.GetPathConst = orig }
}

func applyTestRGWFrontend(spec rgwFrontendSpec, monitors []string, restart bool) (*rgwRollback, error) {
	rb, err := applyRGWFrontend(spec, monitors, restart)
	if err == nil && restart {
		err = finishRGWApply()
	}
	return rb, err
}

// readTestConf returns the current radosgw.conf content.
func readTestConf(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(rgwConfPath())
	require.NoError(t, err)
	return string(data)
}

// TestApplyRGWFrontendFirstEnable verifies the first enable renders
// radosgw.conf with the plaintext default port, starts (not restarts) RGW,
// and leaves no pending-apply marker behind.
func TestApplyRGWFrontendFirstEnable(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()

	rb, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)

	conf := readTestConf(t)
	assert.Contains(t, conf, "port=80", "default plaintext port must be rendered")
	assert.NotContains(t, conf, "ssl_certificate=")

	require.NotNil(t, rb, "a published frontend must be rollback-tracked")
	assert.Nil(t, rb.prevConf, "first enable has no previous config")
	assert.False(t, rb.prevActive, "first enable starts from a stopped member")

	assert.Equal(t, 1, rec.starts, "first enable must start RGW")
	assert.Equal(t, 0, rec.restarts)
	assert.NoFileExists(t, rgwPendingApplyPath(), "a completed apply must clear the pending marker")

	// The keyring symlink is a real prerequisite of a working gateway.
	_, err = os.Lstat(filepath.Join(constants.GetPathConst().ConfPath, "ceph.client.radosgw.gateway.keyring"))
	assert.NoError(t, err)
}

// TestApplyRGWFrontendReapplyNoChange verifies an identical re-apply to a
// healthy gateway is a no-op: no restart, no extra start, nothing published.
func TestApplyRGWFrontendReapplyNoChange(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()

	_, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true // the gateway is up now

	rb, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)
	assert.Nil(t, rb, "identical re-apply must publish nothing")
	assert.Equal(t, 1, rec.starts, "no extra start on re-apply")
	assert.Equal(t, 0, rec.restarts, "no restart on identical re-apply")
}

// TestApplyRGWFrontendMultiMonitorReorderNoRestart is the regression test for
// the idempotency defeat: with more than one monitor (and an IPv6 monitor),
// the rendered `mon host` line must not drive a restart. A re-apply with the
// monitors in a different order, and after the on-disk `mon host` line has
// been rewritten out-of-band (as UpdateConfig->updateRadosGWMonHost does),
// must be a no-op: no restart, nothing republished.
func TestApplyRGWFrontendMultiMonitorReorderNoRestart(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()

	monsA := []string{"10.0.0.3", "10.0.0.1", "fe80::1"}
	monsB := []string{"fe80::1", "10.0.0.1", "10.0.0.3"} // same set, different order

	_, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, monsA, true)
	require.NoError(t, err)
	assert.Equal(t, 1, rec.starts)
	rec.active = true

	// Simulate the periodic UpdateConfig->updateRadosGWMonHost rewrite of the
	// mon host line (sorted + IPv6-bracketed), independent of applyRGWFrontend.
	require.NoError(t, updateRadosGWMonHost(constants.GetPathConst().ConfPath, formatIPv6(monsA)))
	before := readTestConf(t)

	rb, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, monsB, true)
	require.NoError(t, err)
	assert.Nil(t, rb, "re-apply with reordered monitors must publish nothing")
	assert.Equal(t, 0, rec.restarts, "reordered monitors must not restart RGW")

	// The on-disk mon host line (owned by updateRadosGWMonHost) must be intact.
	assert.Equal(t, before, readTestConf(t), "radosgw.conf must be untouched on a no-op re-apply")
	assert.Contains(t, readTestConf(t), "[fe80::1]", "IPv6 mon host must remain bracketed")
}

// TestApplyRGWFrontendStoppedConfiguredRecovery verifies that matching files
// do not mean the gateway is running: a stopped-but-configured member is
// started, without republishing anything.
func TestApplyRGWFrontendStoppedConfiguredRecovery(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()

	_, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)
	before := readTestConf(t)

	rec.active = false // the gateway went down; its files are intact

	rb, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)
	assert.Nil(t, rb, "recovery of a stopped gateway republishes nothing")
	assert.Equal(t, 2, rec.starts, "a stopped-but-configured gateway must be started")
	assert.Equal(t, 0, rec.restarts)
	assert.Equal(t, before, readTestConf(t))
}

// TestApplyRGWFrontendPendingMarkerForcesRedrive verifies the crash window
// between publication and restart: files matching the desired state plus an
// active process do not prove the process loaded them, so the pending-apply
// marker forces the retry to re-drive the service.
func TestApplyRGWFrontendPendingMarkerForcesRedrive(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()

	_, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true
	before := readTestConf(t)

	// Simulate a crash right after publication of a new frontend: the marker
	// survived, so the running process may still serve the previous frontend.
	require.NoError(t, writePendingApplyMarker([]byte(before), true))

	_, err = applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)
	assert.Equal(t, 1, rec.restarts, "the marker must force a restart despite matching files")
	assert.Equal(t, 1, rec.starts)
	assert.Zero(t, rec.probes, "a marker naming the same pair as the config has nothing to probe")
	assert.NoFileExists(t, rgwPendingApplyPath(), "the redrive must clear the marker")
	assert.Equal(t, before, readTestConf(t))
}

// deferRGWRotation enables the gateway with old, then publishes rotated without
// a restart, as "certificate set rgw" without --restart does. The gateway keeps
// serving old and the marker names old as the rollback target. It returns the
// config that named old.
func deferRGWRotation(t *testing.T, rec *rgwOpsRecorder, old, rotated rgwFrontendSpec) string {
	t.Helper()
	_, err := applyTestRGWFrontend(old, []string{"mon1"}, true)
	require.NoError(t, err)
	before := readTestConf(t)
	_, err = applyRGWFrontend(rotated, []string{"mon1"}, false)
	require.NoError(t, err)
	require.FileExists(t, rgwPendingApplyPath(), "a deferred update leaves the marker")
	require.Contains(t, readTestConf(t), rotated.tlsGenDir(), "a deferred update publishes the new pair")
	rec.serves = old.certPEM
	return before
}

// TestApplyRGWFrontendStaleMarkerAfterManualRestart covers the operator who
// defers the restart of a certificate update and then restarts the gateway by
// hand: the marker still names the old pair, but the gateway serves the new
// one. A later change that fails must go back to the pair that was live, not
// to the older one the marker names, and must not delete the live pair.
func TestApplyRGWFrontendStaleMarkerAfterManualRestart(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	old := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	rotated := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certB"), keyPEM: []byte("keyB")}
	broken := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certC"), keyPEM: []byte("keyC")}
	deferRGWRotation(t, rec, old, rotated)
	live := readTestConf(t)

	// The operator restarts the gateway by hand; the daemon does not see it.
	rec.serves = rotated.certPEM

	// A change that does not come up must fall back to what was live.
	rec.readyErr = errRGWInactive
	rb, err := applyTestRGWFrontend(broken, []string{"mon1"}, true)
	require.Error(t, err)
	assert.Nil(t, rb)
	assert.ErrorIs(t, err, ErrPlacementOperationFailed)

	assert.Equal(t, 1, rec.probes, "the gateway must be asked which pair it serves")
	assert.Equal(t, live, readTestConf(t), "rollback must land on the pair the gateway served")
	assert.DirExists(t, rotated.tlsGenDir(), "the live pair must survive the rollback")
	assert.NoDirExists(t, broken.tlsGenDir(), "the unpublished pair must be removed")
	assert.NoDirExists(t, old.tlsGenDir(), "the pair the stale marker named is pruned")
	assert.NoFileExists(t, rgwPendingApplyPath())
	// 1st restart switched to broken, 2nd is the rollback onto rotated.
	assert.Equal(t, 2, rec.restarts)
	assert.Equal(t, 1, rec.starts)
}

// TestApplyRGWFrontendMarkerKeptWhileRestartPending is the same deferred
// update without the manual restart: the gateway still serves the old pair,
// so the marker is right and a failed change goes back to the old pair.
func TestApplyRGWFrontendMarkerKeptWhileRestartPending(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	old := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	rotated := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certB"), keyPEM: []byte("keyB")}
	broken := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certC"), keyPEM: []byte("keyC")}
	before := deferRGWRotation(t, rec, old, rotated)

	rec.readyErr = errRGWInactive
	rb, err := applyTestRGWFrontend(broken, []string{"mon1"}, true)
	require.Error(t, err)
	assert.Nil(t, rb)

	assert.Equal(t, 3, rec.probes, "the probe retries before it gives up")
	assert.Equal(t, before, readTestConf(t), "rollback must land on the pair the marker names")
	assert.DirExists(t, old.tlsGenDir(), "the pair the gateway serves must survive")
	assert.NoDirExists(t, rotated.tlsGenDir(), "a pair that never went live is pruned")
	assert.NoDirExists(t, broken.tlsGenDir())
	assert.NoFileExists(t, rgwPendingApplyPath())
}

// TestApplyRGWFrontendStoppedGatewayIsNotProbed verifies the probe follows the
// liveness reading the rest of the apply uses: a gateway read as stopped is
// started, and the marker stays the rollback target.
func TestApplyRGWFrontendStoppedGatewayIsNotProbed(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	old := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	rotated := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certB"), keyPEM: []byte("keyB")}
	deferRGWRotation(t, rec, old, rotated)
	rec.active = false
	rec.serves = rotated.certPEM

	rb, err := applyTestRGWFrontend(rotated, []string{"mon1"}, true)
	require.NoError(t, err)
	require.NotNil(t, rb)
	assert.Zero(t, rec.probes, "a stopped gateway serves nothing to probe")
	assert.Equal(t, 2, rec.starts, "a stopped gateway is started, not restarted")
	assert.Zero(t, rec.restarts)
	assert.NoFileExists(t, rgwPendingApplyPath())
}

// TestApplyRGWFrontendCompletedMarkerNeedsNoRestart covers a placement apply
// arriving after the operator restarted the gateway by hand: the gateway
// already serves the requested pair, so nothing is restarted and the stale
// marker is dropped.
func TestApplyRGWFrontendCompletedMarkerNeedsNoRestart(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	old := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	rotated := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certB"), keyPEM: []byte("keyB")}
	deferRGWRotation(t, rec, old, rotated)
	rec.serves = rotated.certPEM
	live := readTestConf(t)

	rb, err := applyTestRGWFrontend(rotated, []string{"mon1"}, true)
	require.NoError(t, err)
	assert.Nil(t, rb, "nothing was published, so there is nothing to roll back")
	assert.Equal(t, 1, rec.probes)
	assert.Zero(t, rec.restarts, "a gateway already serving the requested pair is left alone")
	assert.Equal(t, 1, rec.starts)
	assert.Equal(t, live, readTestConf(t))
	assert.NoFileExists(t, rgwPendingApplyPath(), "the stale marker is dropped")
	assert.NoDirExists(t, old.tlsGenDir(), "the superseded pair is pruned")
}

// TestApplyRGWFrontendPortChange verifies a port change rewrites radosgw.conf
// and restarts (not starts) RGW, tracking the previous config for rollback.
func TestApplyRGWFrontendPortChange(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()

	_, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true

	rb, err := applyTestRGWFrontend(rgwFrontendSpec{port: 8080}, []string{"mon1"}, true)
	require.NoError(t, err)
	require.NotNil(t, rb)
	assert.Contains(t, readTestConf(t), "port=8080")
	assert.Contains(t, string(rb.prevConf), "port=80", "the previous config must be kept for rollback")
	assert.True(t, rb.prevActive)

	assert.Equal(t, 1, rec.starts, "start only on first enable")
	assert.Equal(t, 1, rec.restarts, "port change must restart RGW")
}

// TestApplyRGWFrontendTLSEnable verifies TLS material is staged as a
// protected generation (0700 directory, 0600 files), the config references
// it, and a plaintext->TLS transition restarts RGW.
func TestApplyRGWFrontendTLSEnable(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()

	_, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true

	spec := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	genDir := spec.tlsGenDir()

	rb, err := applyTestRGWFrontend(spec, []string{"mon1"}, true)
	require.NoError(t, err)
	require.NotNil(t, rb)

	conf := readTestConf(t)
	assert.Contains(t, conf, "ssl_port=443")
	assert.Contains(t, conf, filepath.Join(genDir, "server.crt"), "config must reference the staged generation")

	cert, err := os.ReadFile(filepath.Join(genDir, "server.crt"))
	require.NoError(t, err)
	assert.Equal(t, "certA", string(cert))
	key, err := os.ReadFile(filepath.Join(genDir, "server.key"))
	require.NoError(t, err)
	assert.Equal(t, "keyA", string(key))

	// 0600 material inside a 0700 directory.
	certInfo, err := os.Stat(filepath.Join(genDir, "server.crt"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), certInfo.Mode().Perm())
	keyInfo, err := os.Stat(filepath.Join(genDir, "server.key"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), keyInfo.Mode().Perm())
	dirInfo, err := os.Stat(genDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm())

	assert.Equal(t, 1, rec.starts, "start only on first enable")
	assert.Equal(t, 1, rec.restarts, "TLS transition must restart")
}

// TestApplyRGWFrontendRotationKeepsPreviousGeneration verifies a rotation
// publishes a new content-addressed generation, restarts, and keeps the old
// generation on disk until the apply is final (prune removes it afterwards).
func TestApplyRGWFrontendRotationKeepsPreviousGeneration(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()

	specA := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	specB := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certB"), keyPEM: []byte("keyB")}
	_, err := applyTestRGWFrontend(specA, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true

	rb, err := applyRGWFrontend(specB, []string{"mon1"}, true)
	require.NoError(t, err)
	require.NotNil(t, rb)
	assert.Equal(t, 1, rec.restarts, "rotation must restart")

	assert.DirExists(t, specA.tlsGenDir(), "the previous generation must survive until the apply is final")
	assert.DirExists(t, specB.tlsGenDir())
	assert.Contains(t, readTestConf(t), specB.tlsGenDir())
	assert.Equal(t, specA.tlsGenDir(), rb.prevGenDir, "rollback must know the previous generation")

	require.NoError(t, finishRGWApply())
	assert.NoDirExists(t, specA.tlsGenDir(), "prune drops the superseded generation")
	assert.DirExists(t, specB.tlsGenDir(), "prune keeps the referenced generation")
}

// TestApplyRGWFrontendRotationRollbackOnRestartFailure verifies that a
// restart failure restores the previous config and removes the unpublished
// generation while keeping the one the restored config references.
func TestApplyRGWFrontendRotationRollbackOnRestartFailure(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{restartErrOn: 1, restartErr: errors.New("snapctl restart failed")}
	defer rec.install(t)()

	specA := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	specB := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certB"), keyPEM: []byte("keyB")}
	_, err := applyTestRGWFrontend(specA, []string{"mon1"}, true)
	require.NoError(t, err)
	before := readTestConf(t)
	rec.active = true

	rb, err := applyTestRGWFrontend(specB, []string{"mon1"}, true)
	require.Error(t, err)
	assert.Nil(t, rb)
	assert.ErrorIs(t, err, ErrPlacementOperationFailed)

	assert.Equal(t, before, readTestConf(t), "the previous config must be restored")
	assert.NoDirExists(t, specB.tlsGenDir(), "the unpublished generation must be removed")
	assert.DirExists(t, specA.tlsGenDir(), "the generation the restored config references must survive")
	assert.NoFileExists(t, rgwPendingApplyPath(), "a rolled-back apply must clear the pending marker")
	// 1st restart failed, 2nd is the rollback restart onto the restored config.
	assert.Equal(t, 2, rec.restarts)
}

// TestApplyRGWFrontendRollbackOnReadinessFailure verifies that a frontend
// which publishes and restarts but does not come up is rolled back: previous
// config restored, staged generation removed, gateway restarted on the old
// frontend.
func TestApplyRGWFrontendRollbackOnReadinessFailure(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()

	_, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)
	before := readTestConf(t)
	rec.active = true
	// The TLS restart will publish and restart but not come up.
	rec.readyErr = errRGWInactive

	spec := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	rb, err := applyTestRGWFrontend(spec, []string{"mon1"}, true)
	require.Error(t, err)
	assert.Nil(t, rb)
	assert.ErrorIs(t, err, ErrPlacementOperationFailed)

	assert.Equal(t, before, readTestConf(t), "the plaintext config must be restored")
	assert.NoDirExists(t, spec.tlsGenDir(), "the unpublished generation must be removed")
	// 1st restart switched to TLS, 2nd is the rollback onto the restored config.
	assert.Equal(t, 2, rec.restarts)
}

// TestApplyRGWFrontendPlaintextRemovesTLSReference verifies a TLS->plaintext
// transition drops the TLS references from the config; the old generation
// stays on disk until the apply is final, then prune removes it.
func TestApplyRGWFrontendPlaintextRemovesTLSReference(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()

	spec := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	_, err := applyTestRGWFrontend(spec, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true

	rb, err := applyRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)
	require.NotNil(t, rb)
	conf := readTestConf(t)
	assert.NotContains(t, conf, "ssl_certificate=")
	assert.Contains(t, conf, "port=80")
	assert.Empty(t, rb.publishedGenDir, "a plaintext apply publishes no generation")

	// Retained for rollback until the apply is final, then prunable.
	assert.DirExists(t, spec.tlsGenDir())
	require.NoError(t, finishRGWApply())
	assert.NoDirExists(t, spec.tlsGenDir())
	assert.Equal(t, 1, rec.restarts)
}

// TestApplyRGWFrontendNoRestartMode checks deferred certificate pickup followed
// by reconciliation: saving material must not restart, but the next apply must.
func TestApplyRGWFrontendNoRestartMode(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	old := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	updated := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certB"), keyPEM: []byte("keyB")}
	_, err := applyTestRGWFrontend(old, []string{"mon1"}, true)
	require.NoError(t, err)
	_, err = applyRGWFrontend(updated, []string{"mon1"}, false)
	require.NoError(t, err)
	assert.Zero(t, rec.restarts)
	assert.Equal(t, 1, rec.starts)
	_, err = applyTestRGWFrontend(updated, []string{"mon1"}, true)
	require.NoError(t, err)
	assert.Equal(t, 1, rec.restarts)
	assert.Contains(t, readTestConf(t), updated.tlsGenDir())
	assert.NoFileExists(t, rgwPendingApplyPath())
}

// TestApplyRGWFrontendRepairsInterruptedGeneration verifies that a generation
// left partial by an interrupted write is completely restaged before the
// config referencing it is published.
func TestApplyRGWFrontendRepairsInterruptedGeneration(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()

	spec := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	genDir := spec.tlsGenDir()

	// A crash left only the certificate behind, with a truncated key.
	require.NoError(t, os.MkdirAll(genDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(genDir, "server.crt"), []byte("certA"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(genDir, "server.key"), []byte("par"), 0644))

	_, err := applyTestRGWFrontend(spec, []string{"mon1"}, true)
	require.NoError(t, err)

	cert, err := os.ReadFile(filepath.Join(genDir, "server.crt"))
	require.NoError(t, err)
	assert.Equal(t, "certA", string(cert))
	key, err := os.ReadFile(filepath.Join(genDir, "server.key"))
	require.NoError(t, err)
	assert.Equal(t, "keyA", string(key), "the interrupted key must be rewritten completely")

	dirInfo, err := os.Stat(genDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm(), "the generation directory must be protected")
}

// TestApplyRGWFrontendChangeKeepsRefreshedMonHost verifies a frontend change
// rewrites only the frontend line: a monitor list refreshed on disk after the
// desired config was rendered must survive the publish.
func TestApplyRGWFrontendChangeKeepsRefreshedMonHost(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()

	_, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"10.0.0.1"}, true)
	require.NoError(t, err)
	rec.active = true

	// The periodic refresher learns about a new monitor before this apply's
	// caller re-reads the monitor list.
	require.NoError(t, updateRadosGWMonHost(constants.GetPathConst().ConfPath, []string{"10.0.0.1", "10.0.0.2"}))

	_, err = applyTestRGWFrontend(rgwFrontendSpec{port: 8080}, []string{"10.0.0.1"}, true)
	require.NoError(t, err)

	conf := readTestConf(t)
	assert.Contains(t, conf, "port=8080", "the frontend change must be published")
	assert.Contains(t, conf, "mon host = 10.0.0.1,10.0.0.2", "a newer monitor line must not be overwritten")
	assert.Equal(t, 1, rec.restarts)
}

// TestApplyRGWFrontendFirstEnableRollback verifies a first enable whose
// gateway never becomes ready is fully undone: no config, no journal, and the
// started process is stopped rather than restarted.
func TestApplyRGWFrontendFirstEnableRollback(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{readyErr: errRGWInactive}
	defer rec.install(t)()

	spec := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	rb, err := applyTestRGWFrontend(spec, []string{"mon1"}, true)
	require.Error(t, err)
	assert.Nil(t, rb)
	assert.ErrorIs(t, err, ErrPlacementOperationFailed)

	assert.NoFileExists(t, rgwConfPath(), "a failed first enable must not leave a config behind")
	assert.NoFileExists(t, rgwPendingApplyPath())
	assert.NoDirExists(t, spec.tlsGenDir(), "the staged generation must be removed")
	assert.Equal(t, 1, rec.starts)
	assert.Equal(t, 1, rec.stops, "the gateway started for a failed first enable must be stopped")
	assert.Equal(t, 0, rec.restarts)
}

// TestCheckRGWReadyHonoursTimeout verifies readiness waits for the configured
// deadline and then reports the last failure.
func TestCheckRGWReadyHonoursTimeout(t *testing.T) {
	origTimeout, origActive := rgwReadyTimeout, checkRGWActiveFunc
	defer func() { rgwReadyTimeout, checkRGWActiveFunc = origTimeout, origActive }()
	rgwReadyTimeout = 300 * time.Millisecond
	checkRGWActiveFunc = func() error { return errRGWInactive }

	start := time.Now()
	err := checkRGWReady(rgwFrontendSpec{port: 80})
	assert.ErrorIs(t, err, errRGWInactive)
	assert.GreaterOrEqual(t, time.Since(start), rgwReadyTimeout)
	assert.Less(t, time.Since(start), 5*time.Second)
}

// TestCheckRGWFrontendPinsCertificate verifies the local readiness probe only
// accepts a listener that serves exactly the staged certificate.
func TestCheckRGWFrontendPinsCertificate(t *testing.T) {
	servedB64, servedKeyB64 := genTestTLSPair(t)
	otherB64, _ := genTestTLSPair(t)
	servedPEM, _ := base64.StdEncoding.DecodeString(servedB64)
	servedKey, _ := base64.StdEncoding.DecodeString(servedKeyB64)
	otherPEM, _ := base64.StdEncoding.DecodeString(otherB64)

	pair, err := tls.X509KeyPair(servedPEM, servedKey)
	require.NoError(t, err)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}})
	require.NoError(t, err)
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = conn.(*tls.Conn).Handshake()
				conn.Close()
			}()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port

	err = checkRGWFrontend(rgwFrontendSpec{ssl: true, sslPort: port, certPEM: servedPEM})
	assert.NoError(t, err, "the served certificate must be accepted")

	err = checkRGWFrontend(rgwFrontendSpec{ssl: true, sslPort: port, certPEM: otherPEM})
	require.Error(t, err, "a different served certificate must be rejected")
	assert.Contains(t, err.Error(), "expected certificate")
}
