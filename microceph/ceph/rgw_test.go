package ceph

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rgwOpsRecorder captures calls to the injectable RGW primitives so tests can
// assert start vs restart and keyring creation without a running snap or Ceph.
type rgwOpsRecorder struct {
	starts   int
	restarts int
	keyrings int
}

func (r *rgwOpsRecorder) startFunc() func() error {
	return func() error { r.starts++; return nil }
}
func (r *rgwOpsRecorder) restartFunc() func() error {
	return func() error { r.restarts++; return nil }
}
func (r *rgwOpsRecorder) keyringFunc() func(string) error {
	return func(string) error { r.keyrings++; return nil }
}

// setupRGWPaths overrides constants.GetPathConst to point at temp directories so
// applyRGWFrontend writes radosgw.conf / SSL files in isolation. Returns a
// restore closure.
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

// setupRGWInjectables replaces the snap/keyring primitives with recorders and
// returns the recorder plus a restore closure.
func setupRGWInjectables(t *testing.T) (*rgwOpsRecorder, func()) {
	t.Helper()
	rec := &rgwOpsRecorder{}
	origStart, origRestart, origKey := startRGWFunc, restartRGWFunc, createRGWKeyringFunc
	startRGWFunc = rec.startFunc()
	restartRGWFunc = rec.restartFunc()
	createRGWKeyringFunc = rec.keyringFunc()
	return rec, func() {
		startRGWFunc = origStart
		restartRGWFunc = origRestart
		createRGWKeyringFunc = origKey
	}
}

func b64(t *testing.T, s string) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// TestEffectiveRGWPorts verifies the default-port-80 rule is applied only for a
// plaintext frontend, and is centralized so the on-disk render and the DB
// record stay consistent.
func TestEffectiveRGWPorts(t *testing.T) {
	tests := []struct {
		name        string
		port, ssl   int
		cert, key   string
		wantPort    int
		wantSSLPort int
	}{
		{"plaintext default", 0, 0, "", "", 80, 0},
		{"plaintext explicit", 8080, 0, "", "", 8080, 0},
		{"plaintext drops stray ssl port", 0, 443, "", "", 80, 0},
		{"plaintext explicit drops stray ssl port", 8080, 443, "", "", 8080, 0},
		{"ssl keeps port 0", 0, 443, "c", "k", 0, 443},
		{"ssl explicit port", 8080, 443, "c", "k", 8080, 443},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, s := effectiveRGWPorts(tc.port, tc.ssl, tc.cert, tc.key)
			assert.Equal(t, tc.wantPort, p)
			assert.Equal(t, tc.wantSSLPort, s)
		})
	}
}

// TestApplyRGWFrontendFirstEnable verifies the first enable renders radosgw.conf
// (with the default port 80 for plaintext), creates the keyring/symlink, and
// starts (not restarts) RGW, reporting changed=true.
func TestApplyRGWFrontendFirstEnable(t *testing.T) {
	restorePaths := setupRGWPaths(t)
	defer restorePaths()
	rec, restoreInj := setupRGWInjectables(t)
	defer restoreInj()

	changed, err := applyRGWFrontend(nil, 0, 0, "", "", []string{"mon1"})
	require.NoError(t, err)
	assert.True(t, changed, "first enable must report changed")

	conf, err := os.ReadFile(filepath.Join(constants.GetPathConst().ConfPath, "radosgw.conf"))
	require.NoError(t, err)
	assert.Contains(t, string(conf), "port=80", "default plaintext port must be rendered")
	assert.NotContains(t, string(conf), "ssl_certificate=")

	assert.Equal(t, 1, rec.starts, "first enable must start RGW")
	assert.Equal(t, 0, rec.restarts)
	assert.Equal(t, 1, rec.keyrings)
}

// TestApplyRGWFrontendReapplyNoChange verifies an identical re-apply is a no-op:
// changed=false, no restart, no extra start, no extra keyring creation.
func TestApplyRGWFrontendReapplyNoChange(t *testing.T) {
	restorePaths := setupRGWPaths(t)
	defer restorePaths()
	rec, restoreInj := setupRGWInjectables(t)
	defer restoreInj()

	_, err := applyRGWFrontend(nil, 80, 0, "", "", []string{"mon1"})
	require.NoError(t, err)

	changed, err := applyRGWFrontend(nil, 80, 0, "", "", []string{"mon1"})
	require.NoError(t, err)
	assert.False(t, changed, "identical re-apply must report no change")
	assert.Equal(t, 1, rec.starts, "no extra start on re-apply")
	assert.Equal(t, 0, rec.restarts, "no restart on identical re-apply")
	assert.Equal(t, 1, rec.keyrings, "no extra keyring creation on re-apply")
}

// TestApplyRGWFrontendMultiMonitorReorderNoRestart is the regression test for
// the idempotency defeat: with more than one monitor (and an IPv6 monitor),
// the rendered `mon host` line must not drive a restart. A re-apply with the
// monitors in a different order, and after the on-disk `mon host` line has been
// rewritten out-of-band (as UpdateConfig->updateRadosGWMonHost does), must be a
// no-op: no restart, changed=false.
func TestApplyRGWFrontendMultiMonitorReorderNoRestart(t *testing.T) {
	restorePaths := setupRGWPaths(t)
	defer restorePaths()
	rec, restoreInj := setupRGWInjectables(t)
	defer restoreInj()

	monsA := []string{"10.0.0.3", "10.0.0.1", "fe80::1"}
	monsB := []string{"fe80::1", "10.0.0.1", "10.0.0.3"} // same set, different order

	changed, err := applyRGWFrontend(nil, 80, 0, "", "", monsA)
	require.NoError(t, err)
	assert.True(t, changed, "first enable must report changed")
	assert.Equal(t, 1, rec.starts)

	// Simulate the periodic UpdateConfig->updateRadosGWMonHost rewrite of the
	// mon host line (sorted + IPv6-bracketed), independent of applyRGWFrontend.
	confPath := filepath.Join(constants.GetPathConst().ConfPath, "radosgw.conf")
	require.NoError(t, updateRadosGWMonHost(constants.GetPathConst().ConfPath, formatIPv6(monsA)))
	before, err := os.ReadFile(confPath)
	require.NoError(t, err)

	// Re-apply the same frontend with monitors in a different order.
	changed, err = applyRGWFrontend(nil, 80, 0, "", "", monsB)
	require.NoError(t, err)
	assert.False(t, changed, "re-apply with reordered monitors must be a no-op")
	assert.Equal(t, 0, rec.restarts, "reordered monitors must not restart RGW")

	// The on-disk mon host line (owned by updateRadosGWMonHost) must be intact.
	after, err := os.ReadFile(confPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "radosgw.conf must be untouched on a no-op re-apply")
	assert.Contains(t, string(after), "[fe80::1]", "IPv6 mon host must remain bracketed")
}

// TestApplyRGWFrontendPortChange verifies a port change rewrites radosgw.conf and
// restarts RGW (not start), and the pre-existing keyring symlink does not error.
func TestApplyRGWFrontendPortChange(t *testing.T) {
	restorePaths := setupRGWPaths(t)
	defer restorePaths()
	rec, restoreInj := setupRGWInjectables(t)
	defer restoreInj()

	_, err := applyRGWFrontend(nil, 80, 0, "", "", []string{"mon1"})
	require.NoError(t, err)

	changed, err := applyRGWFrontend(nil, 8080, 0, "", "", []string{"mon1"})
	require.NoError(t, err)
	assert.True(t, changed, "port change must report changed")

	conf, err := os.ReadFile(filepath.Join(constants.GetPathConst().ConfPath, "radosgw.conf"))
	require.NoError(t, err)
	assert.Contains(t, string(conf), "port=8080")

	assert.Equal(t, 1, rec.starts, "start only on first enable")
	assert.Equal(t, 1, rec.restarts, "port change must restart RGW")
}

// TestApplyRGWFrontendSSLEnable verifies SSL is written, the conf references the
// cert path, and a plaintext->TLS transition restarts RGW.
func TestApplyRGWFrontendSSLEnable(t *testing.T) {
	restorePaths := setupRGWPaths(t)
	defer restorePaths()
	rec, restoreInj := setupRGWInjectables(t)
	defer restoreInj()

	// Start plaintext.
	_, err := applyRGWFrontend(nil, 80, 0, "", "", []string{"mon1"})
	require.NoError(t, err)

	// Transition to TLS.
	changed, err := applyRGWFrontend(nil, 0, 443, b64(t, "cert1"), b64(t, "key1"), []string{"mon1"})
	require.NoError(t, err)
	assert.True(t, changed)

	conf, err := os.ReadFile(filepath.Join(constants.GetPathConst().ConfPath, "radosgw.conf"))
	require.NoError(t, err)
	assert.Contains(t, string(conf), "ssl_port=443")
	assert.Contains(t, string(conf), "ssl_certificate=")

	// SSL files exist on disk.
	_, err = os.ReadFile(filepath.Join(constants.GetPathConst().SSLFilesPath, "server.crt"))
	require.NoError(t, err)

	assert.Equal(t, 1, rec.restarts, "TLS transition must restart")
}

// TestApplyRGWFrontendSSLCertRotation verifies a cert/key rotation (same ports)
// is detected via the cert/key bytes (the conf path is unchanged, so the conf
// bytes are identical) and restarts RGW.
func TestApplyRGWFrontendSSLCertRotation(t *testing.T) {
	restorePaths := setupRGWPaths(t)
	defer restorePaths()
	rec, restoreInj := setupRGWInjectables(t)
	defer restoreInj()

	_, err := applyRGWFrontend(nil, 0, 443, b64(t, "cert1"), b64(t, "key1"), []string{"mon1"})
	require.NoError(t, err)

	changed, err := applyRGWFrontend(nil, 0, 443, b64(t, "cert2"), b64(t, "key2"), []string{"mon1"})
	require.NoError(t, err)
	assert.True(t, changed, "cert rotation must be detected even when conf path is unchanged")

	crt, err := os.ReadFile(filepath.Join(constants.GetPathConst().SSLFilesPath, "server.crt"))
	require.NoError(t, err)
	assert.Equal(t, "cert2", string(crt), "rotated cert must be on disk")

	assert.Equal(t, 1, rec.restarts, "cert rotation must restart")
}

// TestApplyRGWFrontendSSLToPlaintext verifies a TLS->plaintext transition removes
// the leftover SSL files and rewrites the conf without the SSL line.
func TestApplyRGWFrontendSSLToPlaintext(t *testing.T) {
	restorePaths := setupRGWPaths(t)
	defer restorePaths()
	rec, restoreInj := setupRGWInjectables(t)
	defer restoreInj()

	_, err := applyRGWFrontend(nil, 0, 443, b64(t, "cert1"), b64(t, "key1"), []string{"mon1"})
	require.NoError(t, err)

	changed, err := applyRGWFrontend(nil, 80, 0, "", "", []string{"mon1"})
	require.NoError(t, err)
	assert.True(t, changed)

	conf, err := os.ReadFile(filepath.Join(constants.GetPathConst().ConfPath, "radosgw.conf"))
	require.NoError(t, err)
	assert.NotContains(t, string(conf), "ssl_certificate=")
	assert.Contains(t, string(conf), "port=80")

	_, err = os.Stat(filepath.Join(constants.GetPathConst().SSLFilesPath, "server.crt"))
	assert.True(t, os.IsNotExist(err), "leftover cert must be removed on TLS->plaintext")
	_, err = os.Stat(filepath.Join(constants.GetPathConst().SSLFilesPath, "server.key"))
	assert.True(t, os.IsNotExist(err), "leftover key must be removed on TLS->plaintext")

	assert.Equal(t, 1, rec.restarts)
}

// TestApplyRGWFrontendBadBase64 verifies malformed SSL material surfaces a clear
// error (maps to HTTP 400 at the API layer) and reports no change.
func TestApplyRGWFrontendBadBase64(t *testing.T) {
	restorePaths := setupRGWPaths(t)
	defer restorePaths()
	_, restoreInj := setupRGWInjectables(t)
	defer restoreInj()

	changed, err := applyRGWFrontend(nil, 0, 443, "not-base64!!", b64(t, "key1"), []string{"mon1"})
	require.Error(t, err)
	assert.False(t, changed)
	assert.Contains(t, err.Error(), "SSL certificate", "error must identify the bad material")
	assert.ErrorIs(t, err, ErrRgwFrontendInvalid, "malformed TLS must map to a client-side error")
}
