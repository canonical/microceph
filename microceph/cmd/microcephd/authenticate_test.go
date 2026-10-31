package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"testing"
	"time"
)

func TestOnAuthenticate(t *testing.T) {
	now := time.Now()
	trusted := &x509.Certificate{Raw: []byte("trusted"), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	untrusted := &x509.Certificate{Raw: []byte("untrusted"), NotBefore: trusted.NotBefore, NotAfter: trusted.NotAfter}
	expired := *trusted
	expired.NotAfter = now.Add(-time.Minute)
	future := *trusted
	future.NotBefore = now.Add(time.Minute)
	trustStore := map[string]x509.Certificate{"member": *trusted}

	tests := []struct {
		name    string
		tls     *tls.ConnectionState
		trusted map[string]x509.Certificate
		want    bool
		wantErr string
	}{
		{name: "no TLS", trusted: trustStore, wantErr: "no TLS connection state"},
		{name: "no certificates", tls: &tls.ConnectionState{}, trusted: trustStore, wantErr: "no certificate present in request"},
		{name: "trusted leaf", tls: &tls.ConnectionState{PeerCertificates: []*x509.Certificate{trusted}}, trusted: trustStore, want: true},
		{name: "untrusted leaf", tls: &tls.ConnectionState{PeerCertificates: []*x509.Certificate{untrusted}}, trusted: trustStore},
		{name: "trusted certificate after untrusted leaf", tls: &tls.ConnectionState{PeerCertificates: []*x509.Certificate{untrusted, trusted}}, trusted: trustStore},
		{name: "trusted leaf with extra certificate", tls: &tls.ConnectionState{PeerCertificates: []*x509.Certificate{trusted, untrusted}}, trusted: trustStore, want: true},
		{name: "expired trusted leaf", tls: &tls.ConnectionState{PeerCertificates: []*x509.Certificate{&expired}}, trusted: trustStore, wantErr: "certificate has expired"},
		{name: "future trusted leaf", tls: &tls.ConnectionState{PeerCertificates: []*x509.Certificate{&future}}, trusted: trustStore, wantErr: "certificate is not yet valid"},
		{name: "nil trust store", tls: &tls.ConnectionState{PeerCertificates: []*x509.Certificate{trusted}}},
		{name: "empty trust store", tls: &tls.ConnectionState{PeerCertificates: []*x509.Certificate{trusted}}, trusted: map[string]x509.Certificate{}},
		{name: "fingerprint alone is insufficient", tls: &tls.ConnectionState{PeerCertificates: []*x509.Certificate{untrusted}}, trusted: map[string]x509.Certificate{"member": *trusted, "untrusted": *trusted}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := OnAuthenticate(context.Background(), nil, &http.Request{TLS: tt.tls}, tt.trusted)
			if got != tt.want {
				t.Errorf("authenticated = %v, want %v", got, tt.want)
			}

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
