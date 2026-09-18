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
	"testing"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microceph/microceph/api/types"
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
