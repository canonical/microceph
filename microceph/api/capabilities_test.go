package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCapabilitiesGet verifies that cmdCapabilitiesGet returns the CE142
// capability markers, including placement-rgw for object-shaped RGW placement.
func TestCapabilitiesGet(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/1.0/cluster/capabilities", nil)

	resp := cmdCapabilitiesGet(nil, req)
	_ = resp.Render(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var raw struct {
		Metadata types.Capabilities `json:"metadata"`
	}
	err := json.NewDecoder(rec.Body).Decode(&raw)
	require.NoError(t, err)

	assert.ElementsMatch(t, CapabilitiesSupported, raw.Metadata.Supported)
	assert.Contains(t, raw.Metadata.Supported, "deferred-ceph-bootstrap")
	assert.Contains(t, raw.Metadata.Supported, "ceph-only-bootstrap")
	assert.Contains(t, raw.Metadata.Supported, "declarative-placement")
	assert.Contains(t, raw.Metadata.Supported, "placement-rgw", "object-shaped RGW placement capability must be advertised")
}
