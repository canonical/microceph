package ceph

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPopulateParamsBindAddress verifies the bind address is optional,
// canonicalised, and rejected as a client error when it is not an IP.
func TestPopulateParamsBindAddress(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		err     bool
		want    string
	}{
		{name: "omitted", payload: `{"Port":80}`, want: ""},
		{name: "ipv4", payload: `{"BindAddress":"10.75.254.1","Port":8888}`, want: "10.75.254.1"},
		{name: "ipv6 canonicalised", payload: `{"BindAddress":"FD00:0:0::1","Port":8888}`, want: "fd00::1"},
		{name: "hostname", payload: `{"BindAddress":"rgw.example.com","Port":8888}`, err: true},
		{name: "address with port", payload: `{"BindAddress":"10.75.254.1:8888"}`, err: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sp := &RgwServicePlacement{}
			err := sp.PopulateParams(nil, tc.payload)
			if tc.err {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrRgwFrontendInvalid, "a bad address must stay client-classified")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, sp.BindAddress)
		})
	}
}

// TestApplyRGWFrontendBindAddress verifies a bind address renders beast
// endpoint options, survives an identical re-apply without a restart, and
// that dropping it goes back to listening on every address.
func TestApplyRGWFrontendBindAddress(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()

	spec := rgwFrontendSpec{bindAddr: "10.75.254.1", port: 8888}
	_, err := applyTestRGWFrontend(spec, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true
	assert.Contains(t, readTestConf(t), "rgw frontends = beast endpoint=10.75.254.1:8888\n")

	parsed, err := rgwFrontendFromConfig([]byte(readTestConf(t)))
	require.NoError(t, err)
	assert.Equal(t, spec, parsed, "recovery must read back the bound listener")

	rb, err := applyTestRGWFrontend(spec, []string{"mon1"}, true)
	require.NoError(t, err)
	assert.Nil(t, rb, "identical re-apply must publish nothing")
	assert.Equal(t, 0, rec.restarts)

	_, err = applyTestRGWFrontend(rgwFrontendSpec{port: 8888}, []string{"mon1"}, true)
	require.NoError(t, err)
	assert.Contains(t, readTestConf(t), "rgw frontends = beast port=8888\n")
	assert.Equal(t, 1, rec.restarts, "changing the bind address must restart RGW")
}

// TestApplyRGWFrontendBindAddressIPv6TLS verifies IPv6 addresses are
// bracketed and that the TLS listener binds to the same address.
func TestApplyRGWFrontendBindAddressIPv6TLS(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()

	spec := rgwFrontendSpec{bindAddr: "fd00::1", port: 8080, sslPort: 8443, ssl: true, certPEM: []byte("certA"), keyPEM: []byte("keyA")}
	_, err := applyTestRGWFrontend(spec, []string{"mon1"}, true)
	require.NoError(t, err)

	conf := readTestConf(t)
	assert.Contains(t, conf, "rgw frontends = beast endpoint=[fd00::1]:8080 ssl_endpoint=[fd00::1]:8443 ssl_certificate=")
	assert.NotContains(t, conf, " port=")
	assert.NotContains(t, conf, "ssl_port=")
}

// TestRGWConfListenerParsing verifies ports and the bind address are read
// from both frontend forms, and a malformed endpoint is rejected.
func TestRGWConfListenerParsing(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		err         bool
		wantAddr    string
		wantPort    int
		wantSSLPort int
	}{
		{name: "ports", line: "rgw frontends = beast port=80 ssl_port=443 ssl_certificate=/c ssl_private_key=/k",
			wantPort: 80, wantSSLPort: 443},
		{name: "ipv4 endpoints", line: "rgw frontends = beast endpoint=10.0.0.5:80 ssl_endpoint=10.0.0.5:443 ssl_certificate=/c ssl_private_key=/k",
			wantAddr: "10.0.0.5", wantPort: 80, wantSSLPort: 443},
		{name: "ipv6 tls only", line: "rgw frontends = beast ssl_endpoint=[fd00::1]:443 ssl_certificate=/c ssl_private_key=/k",
			wantAddr: "fd00::1", wantSSLPort: 443},
		{name: "endpoint without port", line: "rgw frontends = beast endpoint=10.0.0.5", err: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conf := []byte("[client.radosgw.gateway]\n" + tc.line + "\n")
			port, sslPort, err := rgwConfPorts(conf)
			if tc.err {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantPort, port)
			assert.Equal(t, tc.wantSSLPort, sslPort)
			assert.Equal(t, tc.wantAddr, rgwConfBindAddress(conf))
		})
	}
}

// TestRGWProbeHost verifies the readiness probe dials the bound address, and
// loopback when the gateway listens on every address.
func TestRGWProbeHost(t *testing.T) {
	assert.Equal(t, "127.0.0.1", rgwProbeHost(""))
	assert.Equal(t, "127.0.0.1", rgwProbeHost("0.0.0.0"))
	assert.Equal(t, "::1", rgwProbeHost("::"))
	assert.Equal(t, "10.75.254.1", rgwProbeHost("10.75.254.1"))
	assert.Equal(t, "fd00::1", rgwProbeHost("fd00::1"))
}

// TestCheckRGWFrontendDialsBindAddress verifies readiness reaches a gateway
// bound to a non-default address, which never answers on 127.0.0.1.
func TestCheckRGWFrontendDialsBindAddress(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skipf("127.0.0.2 is not usable on this host: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-amz-request-id", "tx-rgw")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><RequestId>tx-rgw</RequestId></Error>`))
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	require.NoError(t, checkRGWFrontend(rgwFrontendSpec{bindAddr: "127.0.0.2", port: port}))
	require.Error(t, checkRGWFrontend(rgwFrontendSpec{port: port}), "loopback is not the bound address")
}

// TestUpdateRGWCertificatesKeepsBindAddress verifies a certificate rotation
// keeps the listener bound to the address the gateway already uses.
func TestUpdateRGWCertificatesKeepsBindAddress(t *testing.T) {
	defer setupRGWPaths(t)()
	rec := &rgwOpsRecorder{}
	defer rec.install(t)()
	si := newRGWTestState(t, nil)

	certA, keyA := genTestTLSPair(t)
	certAPEM, _ := base64.StdEncoding.DecodeString(certA)
	keyAPEM, _ := base64.StdEncoding.DecodeString(keyA)
	specA := rgwFrontendSpec{bindAddr: "10.75.254.1", sslPort: 8443, ssl: true, certPEM: certAPEM, keyPEM: keyAPEM}
	_, err := applyTestRGWFrontend(specA, []string{"mon1"}, true)
	require.NoError(t, err)
	rec.active = true

	certB, keyB := genTestTLSPair(t)
	require.NoError(t, UpdateRGWCertificates(context.Background(), si, certB, keyB, true))
	assert.Contains(t, readTestConf(t), "rgw frontends = beast ssl_endpoint=10.75.254.1:8443 ssl_certificate=")
}
