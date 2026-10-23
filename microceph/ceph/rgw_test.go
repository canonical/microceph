package ceph

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/interfaces"
)

// rgwOpsRecorder captures injectable service operations without a running snap.
// readyFailures targets a particular readiness gate, not the preceding recovery.
type rgwOpsRecorder struct {
	starts   int
	restarts int
	stops    int
	keyrings int

	active        bool
	readyErr      error
	readyChecks   int
	readyFailures map[int]error

	checkAfterAction bool

	startErr   error
	stopErr    error
	restartErr error
	// restartErrOn fails the Nth restart call (1-based, 0 = none).
	restartErrOn int
}

// install swaps the injectable primitives for recorder-backed fakes and
// returns a restore closure. getConfigDbFunc is redirected to a static
// monitor list so pipeline tests need no dqlite.
func (r *rgwOpsRecorder) install(t *testing.T) func() {
	t.Helper()
	origStart, origRestart, origStop := startRGWFunc, restartRGWFunc, stopRGWFunc
	origCheck, origKey, origCfg := checkRGWActiveFunc, createRGWKeyringFunc, getConfigDbFunc
	origFetch, origActive, origBackward := fetchConfigDb, getActiveMonitorMembersFunc, backwardCompatMonitorsFunc
	origReady := checkRGWReadyFunc
	checkRGWReadyFunc = func(rgwFrontendSpec) error {
		r.readyChecks++
		err := r.readyFailures[r.readyChecks]
		if err != nil {
			r.active = false
			return err
		}
		return checkRGWActiveFunc()
	}

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
	fetchConfigDb = func(context.Context, interfaces.StateInterface) (map[string]string, error) {
		return map[string]string{"mon.host.a": "10.0.0.1"}, nil
	}
	getActiveMonitorMembersFunc = func(context.Context, interfaces.StateInterface) (map[string]struct{}, error) {
		return map[string]struct{}{"a": {}}, nil
	}
	backwardCompatMonitorsFunc = func(context.Context, interfaces.StateInterface) ([]string, error) {
		return []string{"10.0.0.1"}, nil
	}
	return func() {
		startRGWFunc, restartRGWFunc, stopRGWFunc = origStart, origRestart, origStop
		checkRGWActiveFunc, createRGWKeyringFunc, getConfigDbFunc = origCheck, origKey, origCfg
		fetchConfigDb, getActiveMonitorMembersFunc, backwardCompatMonitorsFunc = origFetch, origActive, origBackward
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

// deferRGWRotation starts the old gateway, then publishes rotated without an
// automatic restart. The marker names old and the config names rotated.
func deferRGWRotation(t *testing.T, _ *rgwOpsRecorder, old, rotated rgwFrontendSpec) string {
	t.Helper()
	_, err := applyTestRGWFrontend(old, []string{"mon1"}, true)
	require.NoError(t, err)
	before := readTestConf(t)
	_, err = applyRGWFrontend(rotated, []string{"mon1"}, false)
	require.NoError(t, err)
	require.FileExists(t, rgwPendingApplyPath(), "a deferred update leaves the marker")
	require.Contains(t, readTestConf(t), rotated.tlsGenDir(), "a deferred update publishes the new pair")
	return before
}

func testRGWSpec(t *testing.T) rgwFrontendSpec {
	t.Helper()
	// Recovery reconstructs the on-disk TLS pair and validates it.
	certB64, keyB64 := genTestTLSPair(t)
	cert, err := base64.StdEncoding.DecodeString(certB64)
	require.NoError(t, err)
	key, err := base64.StdEncoding.DecodeString(keyB64)
	require.NoError(t, err)
	return rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: cert, keyPEM: key}
}

// A manual activation of B cannot be inferred from a leaf probe. Recovery
// restarts onto B before applying C, and a failed C rolls back to B, not A.
func TestApplyRGWFrontendStaleMarkerAfterManualRestart(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	a, b, c := testRGWSpec(t), testRGWSpec(t), testRGWSpec(t)
	deferRGWRotation(t, rec, a, b)
	bConf := readTestConf(t)
	// The operator has already activated B; the recovery still verifies it.
	rec.readyFailures = map[int]error{3: errRGWInactive}
	rb, err := applyTestRGWFrontend(c, []string{"mon1"}, true)
	require.Error(t, err)
	assert.Nil(t, rb)
	assert.ErrorIs(t, err, ErrPlacementOperationFailed)
	assert.Equal(t, bConf, readTestConf(t))
	assert.Equal(t, 3, rec.restarts, "recover B, attempt C, restore B")
	assert.Equal(t, 4, rec.readyChecks, "recovery and rollback have independent readiness gates")
	assert.DirExists(t, b.tlsGenDir())
	assert.NoDirExists(t, c.tlsGenDir())
	assert.NoDirExists(t, a.tlsGenDir(), "confirmed rollback can prune older generations")
	assert.NoFileExists(t, rgwPendingApplyPath())
}

// A deferred C must not be published if recovery of B fails. Confirmed
// restoration of A permits cleanup, but the request still fails.
func TestApplyRGWFrontendMarkerKeptWhileRestartPending(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	a, b, c := testRGWSpec(t), testRGWSpec(t), testRGWSpec(t)
	aConf := deferRGWRotation(t, rec, a, b)
	rec.readyFailures = map[int]error{2: errRGWInactive}
	rb, err := applyRGWFrontend(c, []string{"mon1"}, false)
	require.Error(t, err)
	assert.Nil(t, rb)
	assert.Equal(t, aConf, readTestConf(t), "failed recovery restores A")
	assert.Equal(t, 2, rec.restarts, "recover B then restore A")
	assert.Equal(t, 3, rec.readyChecks, "verify restoration before cleanup")
	assert.NoFileExists(t, rgwPendingApplyPath())
	assert.DirExists(t, a.tlsGenDir())
	assert.NoDirExists(t, b.tlsGenDir())
	assert.NoDirExists(t, c.tlsGenDir())
}

// Recovery starts a stopped gateway on its published B configuration.
func TestApplyRGWFrontendStoppedGatewayRecovery(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	a, b := testRGWSpec(t), testRGWSpec(t)
	deferRGWRotation(t, rec, a, b)
	rec.active = false
	rb, err := applyTestRGWFrontend(b, []string{"mon1"}, true)
	require.NoError(t, err)
	assert.Nil(t, rb)
	assert.Equal(t, 2, rec.starts)
	assert.Zero(t, rec.restarts)
	assert.NoFileExists(t, rgwPendingApplyPath())
}

// An identical request after a manual activation still performs one controlled
// restart to verify B, then becomes a no-op on subsequent requests.
func TestApplyRGWFrontendIdenticalDeferredRequestRecoversOnce(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	a, b := testRGWSpec(t), testRGWSpec(t)
	deferRGWRotation(t, rec, a, b)
	live := readTestConf(t)
	rb, err := applyTestRGWFrontend(b, []string{"mon1"}, false)
	require.NoError(t, err)
	assert.Nil(t, rb)
	assert.Equal(t, 1, rec.restarts)
	assert.Equal(t, live, readTestConf(t))
	assert.NoFileExists(t, rgwPendingApplyPath())
	assert.NoDirExists(t, a.tlsGenDir(), "the identical certificate request is complete")
	rb, err = applyTestRGWFrontend(b, []string{"mon1"}, true)
	require.NoError(t, err)
	assert.Nil(t, rb)
	assert.Equal(t, 1, rec.restarts, "second request has no marker to recover")
}

// Pre-upgrade journals, with or without obsolete probe_safe, receive the
// same controlled restart rather than relying on the obsolete hint.
func TestApplyRGWFrontendLegacyMarkerRequiresRestart(t *testing.T) {
	for _, hint := range []any{nil, true, false} {
		t.Run(fmt.Sprint(hint), func(t *testing.T) {
			defer setupRGWPaths(t)()
			rec := &rgwOpsRecorder{}
			defer rec.install(t)()
			a, b := testRGWSpec(t), testRGWSpec(t)
			aConf := deferRGWRotation(t, rec, a, b)
			legacy := map[string]any{"previous_config": aConf, "previously_active": true}
			if hint != nil {
				legacy["probe_safe"] = hint
			}
			data, err := json.Marshal(legacy)
			require.NoError(t, err)
			require.NoError(t, writeRGWFile(rgwPendingApplyPath(), data, 0600))
			_, err = applyTestRGWFrontend(b, []string{"mon1"}, true)
			require.NoError(t, err)
			assert.Equal(t, 1, rec.restarts)
			assert.NoFileExists(t, rgwPendingApplyPath())
		})
	}
}

// testRGWSameLeafDifferentIssuers returns two bundles with the same server
// certificate and private key. The first has an expired intermediate issuer;
// the second has a renewed intermediate with a different certificate but the
// same issuer key, so only the chain changes.
func testRGWSameLeafDifferentIssuers(t *testing.T) (rgwFrontendSpec, rgwFrontendSpec) {
	t.Helper()
	// Separate issuer and server keys let us replace only the intermediate.
	issuerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	// Both issuer certificates use the same subject and public key. Only the
	// first has expired, so the leaf's signature works with either version.
	now := time.Now()
	issuer := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test issuer"},
		NotBefore: now.Add(-48 * time.Hour), NotAfter: now.Add(-24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	oldIssuer, err := x509.CreateCertificate(rand.Reader, issuer, issuer, &issuerKey.PublicKey, issuerKey)
	require.NoError(t, err)
	renewed := *issuer
	renewed.SerialNumber = big.NewInt(2)
	renewed.NotAfter = now.Add(24 * time.Hour)
	newIssuer, err := x509.CreateCertificate(rand.Reader, &renewed, &renewed, &issuerKey.PublicKey, issuerKey)
	require.NoError(t, err)
	// Sign the leaf once; the two bundles below differ only in their issuer.
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "rgw-test"},
		DNSNames: []string{"rgw-test"}, NotBefore: now.Add(-time.Hour),
		NotAfter: now.Add(12 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, issuer, &leafKey.PublicKey, issuerKey)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	require.NoError(t, err)
	key := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	// RGW receives the leaf first, followed by its intermediate certificate.
	bundle := func(issuerDER []byte) rgwFrontendSpec {
		cert := append(bytes.Clone(leafPEM), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issuerDER})...)
		return rgwFrontendSpec{ssl: true, certPEM: cert, keyPEM: key}
	}
	return bundle(oldIssuer), bundle(newIssuer)
}

// TestApplyRGWFrontendChainRenewal restarts when the old and new bundles
// share a leaf: a real TLS handshake against the old chain passes leaf pinning.
func TestApplyRGWFrontendChainRenewal(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	old, rotated := testRGWSameLeafDifferentIssuers(t)
	oldPair, err := tls.X509KeyPair(old.certPEM, old.keyPEM)
	require.NoError(t, err)
	newPair, err := tls.X509KeyPair(rotated.certPEM, rotated.keyPEM)
	require.NoError(t, err)
	// The loopback listener starts with the old chain. Swapping this pointer
	// models a restart without replacing the listener or changing its port.
	var served atomic.Pointer[tls.Certificate]
	served.Store(&oldPair)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return served.Load(), nil },
	})
	require.NoError(t, err)
	defer listener.Close()
	// Complete handshakes so the local listener exposes its actual chain.
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
	old.sslPort = listener.Addr().(*net.TCPAddr).Port
	rotated.sslPort = old.sslPort
	deferRGWRotation(t, rec, old, rotated)
	// Leaf-only readiness cannot distinguish the renewed intermediate.
	checkRGWReadyFunc = checkRGWReady
	require.NoError(t, checkRGWFrontend(rotated))
	// Reconciliation's restart loads the new chain into our simulated gateway.
	originalRestart := restartRGWFunc
	restartRGWFunc = func() error {
		served.Store(&newPair)
		return originalRestart()
	}

	// Recovery restarts even though the old chain passes leaf-only readiness.
	_, err = applyTestRGWFrontend(rotated, []string{"mon1"}, true)
	require.NoError(t, err)
	assert.Equal(t, 1, rec.restarts)
	assert.NoFileExists(t, rgwPendingApplyPath())
	conn, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{InsecureSkipVerify: true})
	require.NoError(t, err)
	defer conn.Close()
	require.Len(t, conn.ConnectionState().PeerCertificates, 2)
	assert.True(t, bytes.Equal(newPair.Certificate[1], conn.ConnectionState().PeerCertificates[1].Raw), "the renewed intermediate must be served")
}

// TestApplyRGWFrontendSupersededDeferredChain constructs a pre-upgrade crash:
// journal A, on-disk config C, but a manually activated listener still on B.
// B and C share a leaf but have different issuer certificates.
func TestApplyRGWFrontendSupersededDeferredChain(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	a := testRGWSpec(t)
	b, c := testRGWSameLeafDifferentIssuers(t)
	// A has a different leaf; B and C keep the same leaf but use different
	// issuer chains. A leaf-only probe can distinguish A from C, not B from C.
	bPair, err := tls.X509KeyPair(b.certPEM, b.keyPEM)
	require.NoError(t, err)
	cPair, err := tls.X509KeyPair(c.certPEM, c.keyPEM)
	require.NoError(t, err)
	// This listener stands in for RGW after the operator has restarted it onto
	// B. Replacing the served pair later models the reconciliation restart.
	var served atomic.Pointer[tls.Certificate]
	served.Store(&bPair)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return served.Load(), nil },
	})
	require.NoError(t, err)
	defer listener.Close()
	// Complete real TLS handshakes so leaf-only readiness sees B's chain.
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
	// Set up A -> B and reconstruct the legacy C publication directly. A new
	// request cannot publish C while recovery of B is unresolved.
	a.sslPort = listener.Addr().(*net.TCPAddr).Port
	b.sslPort, c.sslPort = a.sslPort, a.sslPort
	aConf := deferRGWRotation(t, rec, a, b)
	bConf := readTestConf(t)
	require.NoError(t, stageRGWTLSGeneration(c.tlsGenDir(), c.certPEM, c.keyPEM))
	cConf := strings.ReplaceAll(bConf, b.tlsGenDir(), c.tlsGenDir())
	require.NoError(t, writeRGWFile(rgwConfPath(), []byte(cConf), 0644))
	legacy, err := json.Marshal(map[string]any{"previous_config": aConf, "previously_active": true, "probe_safe": false})
	require.NoError(t, err)
	require.NoError(t, writeRGWFile(rgwPendingApplyPath(), legacy, 0600))
	checkRGWReadyFunc = checkRGWReady
	require.NoError(t, checkRGWFrontend(c), "leaf-only readiness accepts B's obsolete issuer")
	// Make a real reconciliation restart switch the listener from B to C.
	originalRestart := restartRGWFunc
	restartRGWFunc = func() error {
		served.Store(&cPair)
		return originalRestart()
	}

	// Recovery must restart onto C despite that matching leaf.
	rb, err := applyRGWFrontend(c, []string{"mon1"}, true)
	require.NoError(t, err)
	assert.Nil(t, rb)
	assert.Equal(t, 1, rec.restarts)
	assert.DirExists(t, a.tlsGenDir(), "recovery must not prune A")
	assert.DirExists(t, b.tlsGenDir(), "recovery must not prune B")
	conn, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{InsecureSkipVerify: true})
	require.NoError(t, err)
	defer conn.Close()
	require.Len(t, conn.ConnectionState().PeerCertificates, 2)
	assert.True(t, bytes.Equal(cPair.Certificate[1], conn.ConnectionState().PeerCertificates[1].Raw), "the renewed issuer must be served")
	require.NoError(t, finishRGWApply())
	assert.NoFileExists(t, rgwPendingApplyPath())
	assert.DirExists(t, c.tlsGenDir())
}

// Damaged recovery material must not be repaired using the incoming request.
// Restore and verify the original frontend before cleaning the failed update.
func TestApplyRGWFrontendInvalidRecoveryMaterial(t *testing.T) {
	for _, tc := range []struct {
		name   string
		file   string
		damage func(string) error
	}{
		{"missing certificate", "server.crt", os.Remove},
		{"invalid certificate", "server.crt", func(path string) error { return os.WriteFile(path, []byte("not a certificate"), 0600) }},
		{"missing key", "server.key", os.Remove},
		{"mismatched key", "server.key", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer setupRGWPaths(t)()
			rec := &rgwOpsRecorder{}
			defer rec.install(t)()
			a, b, c := testRGWSpec(t), testRGWSpec(t), testRGWSpec(t)
			aConf := deferRGWRotation(t, rec, a, b)
			path := filepath.Join(b.tlsGenDir(), tc.file)
			var err error
			if tc.damage == nil {
				err = os.WriteFile(path, c.keyPEM, 0600)
			} else {
				err = tc.damage(path)
			}
			require.NoError(t, err)
			_, err = applyRGWFrontend(c, []string{"mon1"}, false)
			require.Error(t, err)
			assert.Equal(t, aConf, readTestConf(t))
			assert.NoFileExists(t, rgwPendingApplyPath())
			assert.Equal(t, 1, rec.restarts, "only the original frontend is activated")
			assert.DirExists(t, a.tlsGenDir())
			assert.NoDirExists(t, b.tlsGenDir())
			assert.NoDirExists(t, c.tlsGenDir())
		})
	}
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

	specA, specB := testRGWSpec(t), testRGWSpec(t)
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
	old, updated := testRGWSpec(t), testRGWSpec(t)
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

	cases := []struct {
		name    string
		certPEM []byte
		wantErr bool
	}{
		{"matching certificate", servedPEM, false},
		{"private key before certificate", append(append([]byte(nil), servedKey...), servedPEM...), false},
		{"different certificate", otherPEM, true},
		{"only later certificate matches", append(append([]byte(nil), otherPEM...), servedPEM...), true},
		{"no certificate", servedKey, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkRGWFrontend(rgwFrontendSpec{ssl: true, sslPort: port, certPEM: tc.certPEM})
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
