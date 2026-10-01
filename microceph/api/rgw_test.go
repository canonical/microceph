package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/interfaces"
)

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
