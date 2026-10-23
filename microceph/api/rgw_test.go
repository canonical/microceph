package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	lxdAPI "github.com/canonical/lxd/shared/api"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/interfaces"
)

func TestRGWInvalidFrontendSurvivesHTTPTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := cmdEnableServicePut(nil, r)
		err := response.Render(w, r)
		if err != nil {
			t.Errorf("rendering RGW response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	cases := []struct {
		name    string
		payload string
	}{
		{"malformed payload", `{`},
		{"null payload", `null`},
		{"negative port", `{"Port":-1,"SSL":false}`},
		{"TLS port overflow", `{"SSL":true,"SSLPort":65536}`},
		{"same listener port", `{"Port":443,"SSL":true,"SSLPort":443}`},
		{"missing private key", `{"SSL":true,"SSLCertificate":"Y2VydA=="}`},
		{"invalid certificate", `{"SSL":true,"SSLCertificate":"!","SSLPrivateKey":"c2VjcmV0"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(types.EnableService{Name: "rgw", Wait: true, Payload: tc.payload})
			require.NoError(t, err)
			response, err := http.Post(server.URL+"/1.0/services/rgw", "application/json", bytes.NewReader(body))
			require.NoError(t, err)
			assert.Equal(t, http.StatusBadRequest, response.StatusCode)

			// Microcluster reconstructs the error from HTTP status, not error_code.
			_, err = mcTypes.ParseResponse(response)
			require.Error(t, err)
			assert.True(t, lxdAPI.StatusErrorCheck(err, http.StatusBadRequest))
			assert.NotContains(t, err.Error(), "c2VjcmV0")
		})
	}
}

// TestEnableServicePutResponses verifies the handler's HTTP status for placement
// outcomes, including failures that must not be returned as successful responses.
func TestEnableServicePutResponses(t *testing.T) {
	original := servicePlacementHandlerFunc
	t.Cleanup(func() { servicePlacementHandlerFunc = original })

	cases := []struct {
		name string
		err  error
		code int
	}{
		{"success", nil, http.StatusOK},
		{"operational", fmt.Errorf("%w: start failed", ceph.ErrPlacementOperationFailed), http.StatusInternalServerError},
		{"invalid input", fmt.Errorf("%w: bad port", ceph.ErrRgwFrontendInvalid), http.StatusBadRequest},
		{"operational wins over invalid", errors.Join(ceph.ErrRgwFrontendInvalid, ceph.ErrPlacementOperationFailed), http.StatusInternalServerError},
		{"unclassified", errors.New("unexpected"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			servicePlacementHandlerFunc = func(context.Context, interfaces.StateInterface, types.EnableService) error {
				return tc.err
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/1.0/services/rgw", strings.NewReader(`{"name":"rgw","wait":true,"payload":"{}"}`))
			response := cmdEnableServicePut(nil, req)
			require.NoError(t, response.Render(rec, req))
			assert.Equal(t, tc.code, rec.Code)
		})
	}
}

// TestEnableServicePutMalformedJSON verifies decoding failures return HTTP 400
// without attempting to change service placement.
func TestEnableServicePutMalformedJSON(t *testing.T) {
	original := servicePlacementHandlerFunc
	t.Cleanup(func() { servicePlacementHandlerFunc = original })
	servicePlacementHandlerFunc = func(context.Context, interfaces.StateInterface, types.EnableService) error {
		t.Fatal("malformed JSON must not reach service placement")
		return nil
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/1.0/services/rgw", strings.NewReader(`{`))
	response := cmdEnableServicePut(nil, req)
	require.NoError(t, response.Render(rec, req))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
