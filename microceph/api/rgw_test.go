package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/ceph"
)

// TestServiceErrorResponseMapping verifies the error classifier shared by the
// service enable, RGW disable and RGW certificate handlers.
func TestServiceErrorResponseMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
	}{
		{"operational", fmt.Errorf("%w: start failed", ceph.ErrPlacementOperationFailed), http.StatusInternalServerError},
		{"invalid input", fmt.Errorf("%w: bad port", ceph.ErrRgwFrontendInvalid), http.StatusBadRequest},
		{"operational wins over invalid", errors.Join(ceph.ErrRgwFrontendInvalid, ceph.ErrPlacementOperationFailed), http.StatusInternalServerError},
		{"unclassified", errors.New("unexpected"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, "/1.0/services/rgw", nil)
			require.NoError(t, serviceErrorResponse(tc.err).Render(rec, req))
			assert.Equal(t, tc.code, rec.Code)
		})
	}
}
