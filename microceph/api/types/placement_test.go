package types

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRgwPlacementRequiresExplicitIntent(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"enabled":true}`,
		`{"enabled":true,"ssl":null}`,
		`{"enabled":true,"ssl":false,"port":-1}`,
		`{"enabled":true,"ssl":true,"ssl_port":65536}`,
		`{"enabled":true,"ssl":true,"port":443}`,
		`{"enabled":true,"ssl":false,"ssl_private_key":"secret"}`,
	} {
		t.Run(body, func(t *testing.T) {
			var placement RgwPlacement
			err := json.Unmarshal([]byte(body), &placement)
			require.NoError(t, err)
			_, err = placement.Normalized()
			require.Error(t, err)
		})
	}
}

func TestRgwPlacementListenerDefaults(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		port    int
		sslPort int
		ssl     bool
	}{
		{"plaintext", `{"enabled":true,"ssl":false,"ssl_port":443}`, 80, 0, false},
		{"TLS only", `{"enabled":true,"ssl":true}`, 0, 443, true},
		{"dual listeners", `{"enabled":true,"ssl":true,"port":8080,"ssl_port":8443}`, 8080, 8443, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var placement RgwPlacement
			err := json.Unmarshal([]byte(tc.body), &placement)
			require.NoError(t, err)
			normalized, err := placement.Normalized()
			require.NoError(t, err)
			assert.Equal(t, tc.port, normalized.Port)
			assert.Equal(t, tc.sslPort, normalized.SSLPort)
			require.NotNil(t, normalized.SSL)
			assert.Equal(t, tc.ssl, *normalized.SSL)
		})
	}
}

func TestRgwPlacementDisableNeedsNoFrontend(t *testing.T) {
	var placement RgwPlacement
	err := json.Unmarshal([]byte(`{"enabled":false}`), &placement)
	require.NoError(t, err)
	normalized, err := placement.Normalized()
	require.NoError(t, err)
	body, err := json.Marshal(normalized)
	require.NoError(t, err)
	assert.JSONEq(t, `{"enabled":false}`, string(body))
}

func TestRgwPlacementTLSMaterialAndReuse(t *testing.T) {
	cert, key := rgwTestCertificate(t)
	_, otherKey := rgwTestCertificate(t)
	enabled, ssl := true, true
	placement := RgwPlacement{Enabled: &enabled, SSL: &ssl, SSLCertificate: cert, SSLPrivateKey: key}
	normalized, err := placement.Normalized()
	require.NoError(t, err)

	// A redacted policy must still request TLS when the caller submits it again.
	normalized.SSLCertificate = ""
	normalized.SSLPrivateKey = ""
	replayed, err := normalized.Normalized()
	require.NoError(t, err)
	require.NotNil(t, replayed.SSL)
	assert.True(t, *replayed.SSL)
	assert.Equal(t, 443, replayed.SSLPort)
	assert.Zero(t, replayed.Port)

	for _, pair := range []struct {
		name string
		cert string
		key  string
	}{
		{"certificate only", cert, ""},
		{"key only", "", key},
		{"invalid certificate encoding", "!", key},
		{"invalid key encoding", cert, "!"},
		{"invalid certificate content", base64.StdEncoding.EncodeToString([]byte("not a certificate")), key},
		{"mismatched pair", cert, otherKey},
	} {
		t.Run(pair.name, func(t *testing.T) {
			placement.SSLCertificate, placement.SSLPrivateKey = pair.cert, pair.key
			_, err := placement.Normalized()
			require.Error(t, err)
			assert.NotContains(t, err.Error(), key)
			assert.NotContains(t, err.Error(), cert)
		})
	}
}

func rgwTestCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Date(2050, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return base64.StdEncoding.EncodeToString(certPEM), base64.StdEncoding.EncodeToString(keyPEM)
}
