package ceph

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckRGWFrontendHTTP distinguishes usable S3 responses from unrelated
// HTTP listeners and gateway errors without relying on the Server header.
func TestCheckRGWFrontendHTTP(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		requestID string
		body      string
		ready     bool
	}{
		{"anonymous S3 denial", http.StatusForbidden, "tx-rgw", `<Error><Code>AccessDenied</Code><RequestId>tx-rgw</RequestId></Error>`, true},
		{"S3 bucket listing", http.StatusOK, "tx-rgw", `<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Buckets/></ListAllMyBucketsResult>`, true},
		{"unrelated HTTP server", http.StatusOK, "", `<html>healthy</html>`, false},
		{"unrelated access denial", http.StatusForbidden, "", `<Error><Code>AccessDenied</Code></Error>`, false},
		{"non-S3 response with request ID", http.StatusOK, "tx-other", `<html>healthy</html>`, false},
		{"gateway backend unavailable", http.StatusServiceUnavailable, "tx-rgw", `<Error><Code>ServiceUnavailable</Code><RequestId>tx-rgw</RequestId></Error>`, false},
		{"redirect", http.StatusMovedPermanently, "tx-rgw", "", false},
		{"malformed S3 response", http.StatusForbidden, "tx-rgw", `<Error><Code>AccessDenied`, false},
		{"mismatched request ID", http.StatusForbidden, "tx-rgw", `<Error><Code>AccessDenied</Code><RequestId>tx-other</RequestId></Error>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/", r.URL.Path)
				// The service-provider header is configurable; it is not an identity check.
				w.Header().Set("Server", "custom-provider")
				if tc.requestID != "" {
					w.Header().Set("x-amz-request-id", tc.requestID)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			port := server.Listener.Addr().(*net.TCPAddr).Port
			err := checkRGWFrontend(rgwFrontendSpec{port: port})
			if tc.ready {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// TestCheckRGWFrontendRejectsBareTCPListener rejects a port that accepts TCP
// connections but never answers the readiness request.
func TestCheckRGWFrontendRejectsBareTCPListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	// Nothing accepts or reads HTTP: an occupied port is not a ready gateway.
	port := listener.Addr().(*net.TCPAddr).Port
	require.Error(t, checkRGWFrontend(rgwFrontendSpec{port: port}))
}
