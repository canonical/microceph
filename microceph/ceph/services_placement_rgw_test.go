package ceph

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/constants"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// genTestTLSPair creates a real throwaway certificate/key pair so validation
// (base64 + tls.X509KeyPair) runs against genuine PEM material.
func genTestTLSPair(t *testing.T) (certB64, keyB64 string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "rgw-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"rgw-test"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	certB64 = base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyB64 = base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return certB64, keyB64
}

// rgwPayload marshals a member-side RGW placement payload.
func rgwPayload(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// overrideUpdateConfig bypasses database-dependent ceph.conf rendering so the
// placement pipeline can run against a mock state.
func overrideUpdateConfig(t *testing.T) func() {
	t.Helper()
	orig := updateConfigFunc
	updateConfigFunc = func(context.Context, interfaces.StateInterface) error { return nil }
	return func() { updateConfigFunc = orig }
}

// newRGWTestState builds a mock member state whose database transactions
// commit successfully, or fail with dbErr when set.
func newRGWTestState(t *testing.T, dbErr error) *mocks.StateInterface {
	t.Helper()
	si := mocks.NewStateInterface(t)
	state := &mocks.MockState{
		ClusterName: "node-a",
		Cert:        &shared.CertInfo{},
		DBObj: &mocks.MockDB{TxFn: func(ctx context.Context, f func(context.Context, *sql.Tx) error) error {
			if dbErr != nil {
				return dbErr
			}
			// The transaction body needs the microcluster statement registry,
			// unavailable in unit tests; returning nil models a successful
			// commit. The CreateService-409 idempotency inside the body is
			// exercised by the VM campaign against a real database.
			return nil
		}},
	}
	si.On("ClusterState").Return(state).Maybe()
	return si
}

// stageFlatLayoutGateway leaves the member as an older snap did: the pair in
// the flat server.crt/server.key files, referenced by radosgw.conf. The config
// template has not changed since, so rendering it reproduces that file. The
// monitor differs from the recorder's so a re-rendered config is detectable.
func stageFlatLayoutGateway(t *testing.T, port int) (certPEM, keyPEM []byte, flatCert, flatKey string) {
	t.Helper()
	certB64, keyB64 := genTestTLSPair(t)
	certPEM, err := base64.StdEncoding.DecodeString(certB64)
	require.NoError(t, err)
	keyPEM, err = base64.StdEncoding.DecodeString(keyB64)
	require.NoError(t, err)

	paths := constants.GetPathConst()
	flatCert = filepath.Join(paths.SSLFilesPath, "server.crt")
	flatKey = filepath.Join(paths.SSLFilesPath, "server.key")
	require.NoError(t, os.WriteFile(flatCert, certPEM, 0600))
	require.NoError(t, os.WriteFile(flatKey, keyPEM, 0600))
	conf, err := newRadosGWConfig(paths.ConfPath).RenderConfig(map[string]any{
		"runDir": paths.RunPath, "monitors": "10.0.0.9", "rgwPort": port, "sslPort": 443,
		"sslCertificatePath": flatCert, "sslPrivateKeyPath": flatKey,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(rgwConfPath(), conf, 0644))
	return certPEM, keyPEM, flatCert, flatKey
}

// TestPopulateParamsValidation verifies the full member-side payload
// validation: defaults, TLS inference, and every rejected shape maps to the
// client-side sentinel.
func TestPopulateParamsValidation(t *testing.T) {
	certA, keyA := genTestTLSPair(t)
	_, keyB := genTestTLSPair(t)

	tests := []struct {
		name        string
		payload     string
		err         bool // expect a rejection
		wantSSL     bool // expected inferred/explicit TLS intent
		wantPort    int  // expected effective port
		wantSSLPort int  // expected effective SSL port
	}{
		{name: "plaintext defaults", payload: `{"Port":0,"SSLPort":443}`,
			wantPort: 80, wantSSLPort: 0},
		{name: "plaintext explicit", payload: `{"Port":8080,"SSLPort":443,"SSL":false}`,
			wantPort: 8080, wantSSLPort: 0},
		{name: "tls explicit", payload: rgwPayload(t, map[string]any{"Port": 8080, "SSLPort": 8443, "SSL": true, "SSLCertificate": certA, "SSLPrivateKey": keyA}),
			wantSSL: true, wantPort: 8080, wantSSLPort: 8443},
		{name: "tls default ssl port", payload: rgwPayload(t, map[string]any{"SSL": true, "SSLCertificate": certA, "SSLPrivateKey": keyA}),
			wantSSL: true, wantPort: 0, wantSSLPort: 443},
		{name: "tls inferred from material", payload: rgwPayload(t, map[string]any{"SSLCertificate": certA, "SSLPrivateKey": keyA}),
			wantSSL: true, wantPort: 0, wantSSLPort: 443},
		{name: "tls inferred material ssl port default", payload: rgwPayload(t, map[string]any{"SSLPort": 9443, "SSLCertificate": certA, "SSLPrivateKey": keyA}),
			wantSSL: true, wantPort: 0, wantSSLPort: 9443},
		{name: "tls reuse without material", payload: `{"Port":80,"SSLPort":443,"SSL":true}`,
			wantSSL: true, wantPort: 80, wantSSLPort: 443},
		{name: "null payload", payload: `null`, err: true},
		{name: "empty object", payload: `{}`, err: true},
		{name: "garbage", payload: `not json`, err: true},
		{name: "plaintext with material", payload: rgwPayload(t, map[string]any{"SSL": false, "SSLCertificate": certA, "SSLPrivateKey": keyA}), err: true},
		{name: "half pair", payload: rgwPayload(t, map[string]any{"SSL": true, "SSLCertificate": certA}), err: true},
		{name: "bad base64", payload: `{"SSL":true,"SSLCertificate":"%%%","SSLPrivateKey":"==="}`, err: true},
		{name: "mismatched pair", payload: rgwPayload(t, map[string]any{"SSL": true, "SSLCertificate": certA, "SSLPrivateKey": keyB}), err: true},
		{name: "port collision", payload: rgwPayload(t, map[string]any{"Port": 9000, "SSLPort": 9000, "SSL": true, "SSLCertificate": certA, "SSLPrivateKey": keyA}), err: true},
		{name: "negative port", payload: `{"Port":-1}`, err: true},
		{name: "port out of range", payload: `{"Port":65536}`, err: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sp := &RgwServicePlacement{}
			err := sp.PopulateParams(nil, tc.payload)
			if tc.err {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrRgwFrontendInvalid, "rejected payloads must stay client-classified")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, sp.SSL)
			assert.Equal(t, tc.wantSSL, *sp.SSL)
			assert.Equal(t, tc.wantPort, sp.effPort)
			assert.Equal(t, tc.wantSSLPort, sp.effSSLPort)
		})
	}
}

// TestEnablePipelineAlreadyActiveSucceeds is the regression test for the
// hospitality rejection: enabling RGW on a member whose RGW is already
// running and configured must succeed end-to-end (the old generic check
// rejected it), must not restart the healthy gateway, and must not republish
// anything.
func TestEnablePipelineAlreadyActiveSucceeds(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	defer overrideUpdateConfig(t)()
	si := newRGWTestState(t, nil)

	// Bring the member to "already enabled": configured and running.
	_, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true
	before := readTestConf(t)

	err = ServicePlacementHandler(context.Background(), si, types.EnableService{
		Name:    "rgw",
		Wait:    true,
		Payload: rgwPayload(t, map[string]any{"Port": 0, "SSLPort": 443}),
	})
	require.NoError(t, err, "an already-active gateway must be reconciled, not rejected")

	assert.Equal(t, before, readTestConf(t), "a matching re-apply must not rewrite the config")
	assert.Equal(t, 0, rec.restarts, "a healthy unchanged gateway must not restart")
	assert.Equal(t, 1, rec.starts)
	assert.NoFileExists(t, rgwPendingApplyPath())
}

// TestServiceInitReuseFailsClosed verifies ssl=true with no supplied material
// and no valid local pair fails closed (operational error), never silently
// enabling plaintext.
func TestServiceInitReuseFailsClosed(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	si := newRGWTestState(t, nil)

	sp := &RgwServicePlacement{}
	require.NoError(t, sp.PopulateParams(si, rgwPayload(t, map[string]any{"Port": 80, "SSLPort": 443, "SSL": true})))

	err := sp.ServiceInit(context.Background(), si)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPlacementOperationFailed)
	_, statErr := os.Stat(rgwConfPath())
	assert.True(t, os.IsNotExist(statErr), "a failed reuse must not publish a plaintext config")
	assert.Equal(t, 0, rec.starts+rec.restarts, "nothing must be started on a failed reuse")
}

// TestServiceInitReuseKeepsTLS verifies ssl=true with no supplied material
// reuses the local pair: no restart, no republish, TLS retained.
func TestServiceInitReuseKeepsTLS(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	si := newRGWTestState(t, nil)

	// Reuse revalidates the on-disk pair with tls.X509KeyPair, so the staged
	// material must be a genuine PEM pair.
	certB64, keyB64 := genTestTLSPair(t)
	certPEM, _ := base64.StdEncoding.DecodeString(certB64)
	keyPEM, _ := base64.StdEncoding.DecodeString(keyB64)
	spec := rgwFrontendSpec{port: 80, sslPort: 443, ssl: true, certPEM: certPEM, keyPEM: keyPEM}
	_, err := applyTestRGWFrontend(spec, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true
	before := readTestConf(t)

	sp := &RgwServicePlacement{}
	// Same ports, TLS, but no material: reuse the pair the config references.
	require.NoError(t, sp.PopulateParams(si, rgwPayload(t, map[string]any{"Port": 80, "SSLPort": 443, "SSL": true})))
	require.NoError(t, sp.ServiceInit(context.Background(), si))

	assert.Equal(t, before, readTestConf(t), "reuse must keep the TLS configuration")
	assert.Equal(t, 0, rec.restarts, "reuse of the running pair must not restart")
	assert.Equal(t, 1, rec.starts)
}

// TestServiceInitReuseFailsOnUnusableLocalPair verifies reuse revalidates the
// on-disk pair: corrupted local material fails closed instead of being
// adopted.
func TestServiceInitReuseFailsOnUnusableLocalPair(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	si := newRGWTestState(t, nil)

	spec := rgwFrontendSpec{port: 80, sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	_, err := applyTestRGWFrontend(spec, []string{"mon1"}, true)
	require.NoError(t, err)
	// Corrupt the private key the config references (interrupted write).
	require.NoError(t, os.WriteFile(filepath.Join(spec.tlsGenDir(), "server.key"), []byte("garbage"), 0600))
	rec.active = true
	before := readTestConf(t)

	sp := &RgwServicePlacement{}
	require.NoError(t, sp.PopulateParams(si, rgwPayload(t, map[string]any{"Port": 80, "SSLPort": 443, "SSL": true})))

	err = sp.ServiceInit(context.Background(), si)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPlacementOperationFailed)
	assert.Equal(t, before, readTestConf(t), "a failed reuse must leave the config untouched")
	assert.Equal(t, 0, rec.restarts)
}

// TestServiceInitReuseMigratesFlatLayout verifies the upgrade path: a TLS
// apply without material reuses the flat pair, moves it into a generation
// with one restart, and drops the flat files only once the record commits.
func TestServiceInitReuseMigratesFlatLayout(t *testing.T) {
	// The old CLI left the plaintext port out whenever a pair was supplied.
	for name, port := range map[string]int{"TLS only": 0, "dual listeners": 80} {
		t.Run(name, func(t *testing.T) {
			defer setupRGWPaths(t)()
			rec := &rgwOpsRecorder{active: true}
			defer rec.install(t)()
			si := newRGWTestState(t, nil)
			certPEM, keyPEM, flatCert, flatKey := stageFlatLayoutGateway(t, port)
			genDir := rgwFrontendSpec{certPEM: certPEM, keyPEM: keyPEM}.tlsGenDir()
			payload := rgwPayload(t, map[string]any{"Port": port, "SSLPort": 443, "SSL": true})

			sp := &RgwServicePlacement{}
			require.NoError(t, sp.PopulateParams(si, payload))
			require.NoError(t, sp.ServiceInit(context.Background(), si))
			assert.FileExists(t, rgwPendingApplyPath(), "the migration is journaled until the record commits")

			conf := readTestConf(t)
			assert.Contains(t, conf, "ssl_certificate="+filepath.Join(genDir, "server.crt"))
			assert.Contains(t, conf, "ssl_private_key="+filepath.Join(genDir, "server.key"))
			assert.NotContains(t, conf, flatCert, "the config must stop referencing the flat layout")
			assert.Contains(t, conf, "mon host = 10.0.0.9", "only the frontend line may change")
			assert.Equal(t, port != 0, strings.Contains(conf, " port="), "the plaintext listener must stay as it was")
			for file, want := range map[string][]byte{"server.crt": certPEM, "server.key": keyPEM} {
				got, err := os.ReadFile(filepath.Join(genDir, file))
				require.NoError(t, err)
				assert.Equal(t, want, got, "the generation must hold the reused pair unchanged")
				info, err := os.Stat(filepath.Join(genDir, file))
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
			}
			assert.Equal(t, 1, rec.restarts, "the changed paths restart the running gateway once")
			assert.Equal(t, 0, rec.starts)
			assert.FileExists(t, flatCert, "the flat pair is the rollback target until the record commits")
			assert.FileExists(t, flatKey)

			require.NoError(t, sp.DbUpdate(context.Background(), si))
			assert.NoFileExists(t, flatCert, "a committed migration removes the flat pair")
			assert.NoFileExists(t, flatKey)
			assert.DirExists(t, genDir)
			assert.NoFileExists(t, rgwPendingApplyPath())

			// Once migrated, the same request is the ordinary reuse: nothing restarts.
			again := &RgwServicePlacement{}
			require.NoError(t, again.PopulateParams(si, payload))
			require.NoError(t, again.ServiceInit(context.Background(), si))
			assert.Equal(t, conf, readTestConf(t))
			assert.Equal(t, 1, rec.restarts)
		})
	}
}

// TestServiceInitReuseMigrationRollbackKeepsFlatPair verifies a migration
// whose gateway does not come back returns to the flat layout intact.
func TestServiceInitReuseMigrationRollbackKeepsFlatPair(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{active: true, readyErr: errors.New("not ready")}
	defer rec.install(t)()
	si := newRGWTestState(t, nil)
	certPEM, keyPEM, flatCert, flatKey := stageFlatLayoutGateway(t, 0)
	genDir := rgwFrontendSpec{certPEM: certPEM, keyPEM: keyPEM}.tlsGenDir()
	before := readTestConf(t)

	sp := &RgwServicePlacement{}
	require.NoError(t, sp.PopulateParams(si, rgwPayload(t, map[string]any{"SSLPort": 443, "SSL": true})))
	err := sp.ServiceInit(context.Background(), si)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPlacementOperationFailed)

	assert.Equal(t, before, readTestConf(t), "the config must reference the flat pair again")
	for path, want := range map[string][]byte{flatCert: certPEM, flatKey: keyPEM} {
		got, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		assert.Equal(t, want, got, "rollback must leave the flat pair untouched")
	}
	assert.NoDirExists(t, genDir, "the abandoned generation must not linger")
	assert.NoFileExists(t, rgwPendingApplyPath())
	assert.Equal(t, 2, rec.restarts, "one restart onto the generation, one back onto the flat pair")
}

// TestPostPlacementCheckFailureRollsBack verifies the readiness phase restores
// the previous usable frontend when the gateway does not stay up.
func TestPostPlacementCheckFailureRollsBack(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	si := newRGWTestState(t, nil)

	_, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true

	sp := &RgwServicePlacement{}
	require.NoError(t, sp.PopulateParams(si, rgwPayload(t, map[string]any{"Port": 8080})))
	require.NoError(t, sp.ServiceInit(context.Background(), si))
	assert.Contains(t, readTestConf(t), "port=8080")
	checkRGWActiveFunc = func() error { return errRGWInactive }

	err = sp.PostPlacementCheck(si)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPlacementOperationFailed)

	port, sslPort, parseErr := rgwConfPorts([]byte(readTestConf(t)))
	require.NoError(t, parseErr)
	assert.Equal(t, 80, port)
	assert.Zero(t, sslPort)
	// The monitor line on disk is owned by the refresher, not the apply.
	assert.Contains(t, readTestConf(t), "mon host = mon1")
	assert.Equal(t, 2, rec.restarts, "the change restart plus the rollback restart")
	assert.NoFileExists(t, rgwPendingApplyPath())
}

// TestDbUpdateFailureRollsBack verifies a failed database record restores the
// previous frontend, so the database never describes a gateway configuration
// that is not running.
func TestDbUpdateFailureRollsBack(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	si := newRGWTestState(t, errors.New("dqlite unavailable"))

	_, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true

	sp := &RgwServicePlacement{}
	require.NoError(t, sp.PopulateParams(si, rgwPayload(t, map[string]any{"Port": 8080})))
	require.NoError(t, sp.ServiceInit(context.Background(), si))

	err = sp.DbUpdate(context.Background(), si)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPlacementOperationFailed)

	port, sslPort, parseErr := rgwConfPorts([]byte(readTestConf(t)))
	require.NoError(t, parseErr)
	assert.Equal(t, 80, port)
	assert.Zero(t, sslPort)
	// The monitor line on disk is owned by the refresher, not the apply.
	assert.Contains(t, readTestConf(t), "mon host = mon1")
	assert.Equal(t, 2, rec.restarts, "the change restart plus the rollback restart")
	assert.NoFileExists(t, rgwPendingApplyPath())
}

// TestDbUpdateSuccessPrunesGenerations verifies the superseded TLS generation
// is dropped only once the apply is final (record committed).
func TestDbUpdateSuccessPrunesGenerations(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	si := newRGWTestState(t, nil)

	specA := rgwFrontendSpec{port: 80, sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	_, err := applyTestRGWFrontend(specA, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true

	certB64, keyB64 := genTestTLSPair(t)
	sp := &RgwServicePlacement{}
	require.NoError(t, sp.PopulateParams(si, rgwPayload(t, map[string]any{"Port": 80, "SSLPort": 443, "SSL": true, "SSLCertificate": certB64, "SSLPrivateKey": keyB64})))
	require.NoError(t, sp.ServiceInit(context.Background(), si))
	assert.DirExists(t, specA.tlsGenDir(), "the old generation survives until the record commits")

	require.NoError(t, sp.DbUpdate(context.Background(), si))
	assert.NoDirExists(t, specA.tlsGenDir(), "a committed apply prunes the superseded generation")
}

// TestDisableRGWConvergent verifies cleanup retries and preservation of a
// running gateway's configuration when stopping it fails.
func TestDisableRGWConvergent(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	si := newRGWTestState(t, nil)

	spec := rgwFrontendSpec{port: 80, sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	_, err := applyTestRGWFrontend(spec, []string{"mon1"}, true)
	require.NoError(t, err)

	// Simulate the full set of leftovers, including legacy-layout files and
	// an interrupted-apply marker.
	pathConsts := constants.GetPathConst()
	require.NoError(t, os.WriteFile(filepath.Join(pathConsts.SSLFilesPath, "server.crt"), []byte("legacy"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(pathConsts.SSLFilesPath, "server.key"), []byte("legacy"), 0600))
	require.NoError(t, os.WriteFile(rgwPendingApplyPath(), []byte("pending"), 0644))
	keyringDir := filepath.Join(pathConsts.DataPath, "radosgw", "ceph-radosgw.gateway")
	require.NoError(t, os.MkdirAll(keyringDir, 0770))
	require.NoError(t, os.WriteFile(filepath.Join(keyringDir, "keyring"), []byte("key"), 0600))

	require.NoError(t, DisableRGW(context.Background(), si))

	for path, desc := range map[string]string{
		rgwConfPath():         "radosgw.conf",
		rgwPendingApplyPath(): "pending-apply marker",
		rgwTLSRoot():          "TLS generations",
		filepath.Join(pathConsts.SSLFilesPath, "server.crt"):                      "legacy certificate",
		filepath.Join(pathConsts.SSLFilesPath, "server.key"):                      "legacy private key",
		filepath.Join(keyringDir, "keyring"):                                      "keyring",
		filepath.Join(pathConsts.ConfPath, "ceph.client.radosgw.gateway.keyring"): "keyring symlink",
	} {
		_, statErr := os.Stat(path)
		assert.True(t, os.IsNotExist(statErr), "disable must remove the "+desc)
	}
	assert.Equal(t, 1, rec.stops)

	// A repeated disable on an already-clean member is a successful no-op.
	require.NoError(t, DisableRGW(context.Background(), si))
	assert.Equal(t, 2, rec.stops)

	// Never discard a running gateway's configuration when stopping it fails.
	rec.active = true
	rec.stopErr = errors.New("snapctl stop failed")
	require.NoError(t, os.WriteFile(rgwConfPath(), []byte("leftover"), 0644))
	err = DisableRGW(context.Background(), si)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPlacementOperationFailed)
	assert.FileExists(t, rgwConfPath())
	assert.True(t, rec.active)
}

// TestDisableRGWDatabaseFailureLeavesFilesForRetry verifies a failed record
// removal stops the disable before any file is deleted, so the member stays
// consistent (record and files both present) for a retry.
func TestDisableRGWDatabaseFailureLeavesFilesForRetry(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	si := newRGWTestState(t, errors.New("dqlite unavailable"))

	spec := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	_, err := applyTestRGWFrontend(spec, []string{"mon1"}, true)
	require.NoError(t, err)

	err = DisableRGW(context.Background(), si)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPlacementOperationFailed)
	assert.Equal(t, 1, rec.stops)
	assert.FileExists(t, rgwConfPath(), "files must survive a failed record removal")
	assert.DirExists(t, spec.tlsGenDir())
	assert.FileExists(t, filepath.Join(constants.GetPathConst().ConfPath, "ceph.client.radosgw.gateway.keyring"))
}

// TestDbUpdateFinishFailureIsNotAnApplyFailure verifies that once the record
// is committed, a journal that cannot be cleared does not fail the apply or
// undo the published frontend.
func TestDbUpdateFinishFailureIsNotAnApplyFailure(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	si := newRGWTestState(t, nil)

	_, err := applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true

	sp := &RgwServicePlacement{}
	require.NoError(t, sp.PopulateParams(si, rgwPayload(t, map[string]any{"Port": 8080})))
	require.NoError(t, sp.ServiceInit(context.Background(), si))

	// A non-empty directory in the journal's place cannot be removed.
	require.NoError(t, os.Remove(rgwPendingApplyPath()))
	require.NoError(t, os.MkdirAll(filepath.Join(rgwPendingApplyPath(), "stuck"), 0700))

	require.NoError(t, sp.DbUpdate(context.Background(), si))
	assert.Contains(t, readTestConf(t), "port=8080", "the recorded frontend must stay published")
	assert.Equal(t, 1, rec.restarts, "no rollback restart may follow a committed record")
}

// TestRemoveServiceDatabaseToleratesMissingRGWRecord verifies an already
// absent RGW record is a successful removal, while other services keep the
// strict behaviour.
func TestRemoveServiceDatabaseToleratesMissingRGWRecord(t *testing.T) {
	origDelete, origFrontend := deleteServiceRecordFunc, deleteRGWFrontendFunc
	defer func() { deleteServiceRecordFunc, deleteRGWFrontendFunc = origDelete, origFrontend }()
	deleteServiceRecordFunc = func(context.Context, *sql.Tx, string, string) error {
		return api.StatusErrorf(http.StatusNotFound, "Service not found")
	}
	frontendDeletes := 0
	deleteRGWFrontendFunc = func(context.Context, *sql.Tx, string) error {
		frontendDeletes++
		return nil
	}

	si := mocks.NewStateInterface(t)
	si.On("ClusterState").Return(&mocks.MockState{
		ClusterName: "node-a",
		Cert:        &shared.CertInfo{},
		DBObj: &mocks.MockDB{TxFn: func(ctx context.Context, f func(context.Context, *sql.Tx) error) error {
			return f(ctx, nil)
		}},
	}).Maybe()

	require.NoError(t, removeServiceDatabase(context.Background(), si, "rgw"))
	assert.Equal(t, 1, frontendDeletes, "the frontend record is still dropped")

	err := removeServiceDatabase(context.Background(), si, "mds")
	require.Error(t, err, "other services keep reporting a missing record")
}

// TestDbUpdateRecordsEffectiveFrontend verifies the committed record carries
// the member name and the effective listeners, with an existing service row
// treated as success.
func TestDbUpdateRecordsEffectiveFrontend(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	origCreate, origUpsert := createServiceRecordFunc, upsertRGWFrontendFunc
	defer func() { createServiceRecordFunc, upsertRGWFrontendFunc = origCreate, origUpsert }()

	type recorded struct {
		member        string
		port, sslPort int
		ssl           bool
	}
	var got recorded
	createServiceRecordFunc = func(context.Context, *sql.Tx, database.Service) (int64, error) {
		return 0, api.StatusErrorf(http.StatusConflict, "already exists")
	}
	upsertRGWFrontendFunc = func(_ context.Context, _ *sql.Tx, member string, port, sslPort int, ssl bool) error {
		got = recorded{member, port, sslPort, ssl}
		return nil
	}
	si := mocks.NewStateInterface(t)
	si.On("ClusterState").Return(&mocks.MockState{
		ClusterName: "node-a",
		Cert:        &shared.CertInfo{},
		DBObj: &mocks.MockDB{TxFn: func(ctx context.Context, f func(context.Context, *sql.Tx) error) error {
			return f(ctx, nil)
		}},
	}).Maybe()

	certB64, keyB64 := genTestTLSPair(t)
	sp := &RgwServicePlacement{}
	require.NoError(t, sp.PopulateParams(si, rgwPayload(t, map[string]any{"Port": 8080, "SSL": true, "SSLCertificate": certB64, "SSLPrivateKey": keyB64})))
	require.NoError(t, sp.ServiceInit(context.Background(), si))
	require.NoError(t, sp.DbUpdate(context.Background(), si))
	assert.Equal(t, recorded{"node-a", 8080, 443, true}, got)
}

// TestUpdateRGWCertificatesRequiresRunningTLSGateway verifies the certificate
// command refuses a stopped gateway and a plaintext one, without touching
// either.
func TestUpdateRGWCertificatesRequiresRunningTLSGateway(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	si := newRGWTestState(t, nil)
	certB64, keyB64 := genTestTLSPair(t)

	err := UpdateRGWCertificates(context.Background(), si, certB64, keyB64, true)
	require.Error(t, err, "no gateway is running yet")
	assert.ErrorIs(t, err, ErrPlacementOperationFailed)

	_, err = applyTestRGWFrontend(rgwFrontendSpec{port: 80}, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true
	before := readTestConf(t)
	err = UpdateRGWCertificates(context.Background(), si, certB64, keyB64, true)
	require.Error(t, err, "a plaintext gateway has no pair to replace")
	assert.ErrorIs(t, err, ErrPlacementOperationFailed)
	assert.Equal(t, before, readTestConf(t))
	assert.Equal(t, 0, rec.restarts)

	err = UpdateRGWCertificates(context.Background(), si, "!", keyB64, true)
	assert.ErrorIs(t, err, ErrRgwFrontendInvalid, "bad material is a client error")
}

// TestUpdateRGWCertificatesRotatesThePair verifies a rotation with restart
// publishes a new protected generation, restarts once, and finalises the
// apply; a rotation without restart leaves the journal for the next apply.
func TestUpdateRGWCertificatesRotatesThePair(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	si := newRGWTestState(t, nil)

	certA, keyA := genTestTLSPair(t)
	certAPEM, _ := base64.StdEncoding.DecodeString(certA)
	keyAPEM, _ := base64.StdEncoding.DecodeString(keyA)
	specA := rgwFrontendSpec{sslPort: 443, ssl: true, certPEM: certAPEM, keyPEM: keyAPEM}
	_, err := applyTestRGWFrontend(specA, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true

	certB, keyB := genTestTLSPair(t)
	require.NoError(t, UpdateRGWCertificates(context.Background(), si, certB, keyB, true))
	certBPEM, _ := base64.StdEncoding.DecodeString(certB)
	keyBPEM, _ := base64.StdEncoding.DecodeString(keyB)
	genB := rgwFrontendSpec{certPEM: certBPEM, keyPEM: keyBPEM}.tlsGenDir()
	assert.Contains(t, readTestConf(t), genB, "the config must reference the new pair")
	assert.Contains(t, readTestConf(t), "ssl_port=443", "ports are kept from the running config")
	assert.Equal(t, 1, rec.restarts)
	assert.NoFileExists(t, rgwPendingApplyPath())
	assert.NoDirExists(t, specA.tlsGenDir(), "the superseded pair is pruned once the apply is final")
	for _, name := range []string{"server.crt", "server.key"} {
		info, err := os.Stat(filepath.Join(genB, name))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}

	certC, keyC := genTestTLSPair(t)
	require.NoError(t, UpdateRGWCertificates(context.Background(), si, certC, keyC, false))
	assert.Equal(t, 1, rec.restarts, "a deferred update must not restart")
	assert.FileExists(t, rgwPendingApplyPath(), "the journal waits for the next apply")
	assert.DirExists(t, genB, "the pair still loaded stays until the new one is confirmed")
}
