package main

import (
	"context"
	"crypto/subtle"
	"crypto/x509"
	"errors"
	"net/http"
	"time"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

	"github.com/canonical/microceph/microceph/logger"
)

// OnAuthenticate authenticates only the TLS leaf certificate against the cluster trust store.
func OnAuthenticate(_ context.Context, _ mcTypes.State, r *http.Request, trustedCerts map[string]x509.Certificate) (bool, error) {
	if r.TLS == nil {
		return false, errors.New("no TLS connection state")
	}

	if len(r.TLS.PeerCertificates) == 0 {
		return false, errors.New("no certificate present in request")
	}

	// Later certificates are supplied by the peer but do not identify that peer.
	peerCertificate := r.TLS.PeerCertificates[0]
	now := time.Now()
	if now.Before(peerCertificate.NotBefore) {
		return false, errors.New("certificate is not yet valid")
	}

	if now.After(peerCertificate.NotAfter) {
		return false, errors.New("certificate has expired")
	}

	for fingerprint, cert := range trustedCerts {
		if subtle.ConstantTimeCompare(peerCertificate.Raw, cert.Raw) == 1 {
			logger.Debugf("Matched trusted cert: fingerprint=%s subject=%s", fingerprint, cert.Subject.String())
			return true, nil
		}
	}

	return false, nil
}
