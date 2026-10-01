package ceph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/canonical/microceph/microceph/tests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// rgwOpsRecorder captures calls to the injectable RGW primitives so tests can
// assert start vs restart vs stop and keyring creation without a running snap
// or Ceph cluster.
//
// active models the pre-change liveness read; readyErr, when set, is returned
// by every active check after the first one, i.e. the post-action readiness
// gate.
type rgwOpsRecorder struct {
	starts   int
	restarts int
	stops    int
	keyrings int

	active   bool
	readyErr error

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
	origReady := checkRGWReadyFunc
	checkRGWReadyFunc = func(rgwFrontendSpec) error { return checkRGWActiveFunc() }

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
		checkRGWReadyFunc = origReady
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
	assert.NoFileExists(t, rgwPendingApplyPath(), "the redrive must clear the marker")
	assert.Equal(t, before, readTestConf(t))
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

// The mock-runner suite below predates the recorder-based tests above. Only its
// disable test is left; it goes when disable gets its own convergence tests.
type rgwSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

func TestRGW(t *testing.T) {
	suite.Run(t, new(rgwSuite))
}

// Expect: run snapctl service stop
func addStopRGWExpectations(s *rgwSuite, r *mocks.Runner) {
	u := api.NewURL()

	state := &mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
	}

	s.TestStateInterface.On("ClusterState").Return(state)
	r.On("RunCommand", tests.CmdAny("snapctl", 3)...).Return("ok", nil).Once()
}

// Set up test suite
func (s *rgwSuite) SetupTest() {
	s.BaseSuite.SetupTest()
	s.CopyCephConfigs()

	s.TestStateInterface = mocks.NewStateInterface(s.T())
}

func (s *rgwSuite) TestDisableRGW() {
	r := mocks.NewRunner(s.T())

	addStopRGWExpectations(s, r)

	common.ProcessExec = r

	err := DisableRGW(context.Background(), s.TestStateInterface)

	// we expect a missing database error
	assert.EqualError(s.T(), err, "no server certificate")

	// check that the radosgw.conf file is absent
	_, err = os.Stat(filepath.Join(s.Tmp, "SNAP_DATA", "conf", "radosgw.conf"))
	assert.True(s.T(), os.IsNotExist(err))

	// check that the keyring file is absent
	_, err = os.Stat(filepath.Join(s.Tmp, "SNAP_COMMON", "data", "radosgw", "ceph-radosgw.gateway", "keyring"))
	assert.True(s.T(), os.IsNotExist(err))
}
