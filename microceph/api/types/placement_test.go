package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRgwPlacementUnmarshalObject confirms the Option B wire shape: the rgw
// field is an object carrying enabled + charm-derived port/TLS material. A
// bare bool is rejected (clean break; the charm never shipped with the bool
// flag).
func TestRgwPlacementUnmarshalObject(t *testing.T) {
	raw := `{
		"mode": "reconcile",
		"members": {
			"node-a": {
				"rgw": {
					"enabled": true,
					"port": 8080,
					"ssl_port": 8443,
					"ssl_certificate": "Y2VydA==",
					"ssl_private_key": "a2V5"
				}
			}
		}
	}`
	var policy PlacementPolicy
	err := json.Unmarshal([]byte(raw), &policy)
	require.NoError(t, err)
	require.Contains(t, policy.Members, "node-a")

	rgw := policy.Members["node-a"].Rgw
	require.NotNil(t, rgw)
	assert.True(t, rgw.Enabled)
	assert.Equal(t, 8080, rgw.Port)
	assert.Equal(t, 8443, rgw.SSLPort)
	assert.Equal(t, "Y2VydA==", rgw.SSLCertificate)
	assert.Equal(t, "a2V5", rgw.SSLPrivateKey)
}

// TestRgwPlacementBareBoolRejected confirms the clean break: a bare rgw bool
// no longer decodes. DisallowUnknownFields is the API decoder's gate; at the
// type level a bool into *RgwPlacement must fail to unmarshal.
func TestRgwPlacementBareBoolRejected(t *testing.T) {
	raw := `{"mode":"reconcile","members":{"node-a":{"rgw":true}}}`
	var policy PlacementPolicy
	err := json.Unmarshal([]byte(raw), &policy)
	require.Error(t, err, "a bare rgw bool must be rejected (clean break)")
}

// TestRgwPlacementOmittedIsNil confirms that an omitted rgw field leaves the
// pointer nil (untouched), distinct from {enabled:false} (remove intent).
func TestRgwPlacementOmittedIsNil(t *testing.T) {
	raw := `{"mode":"reconcile","members":{"node-a":{"control":true}}}`
	var policy PlacementPolicy
	err := json.Unmarshal([]byte(raw), &policy)
	require.NoError(t, err)
	assert.Nil(t, policy.Members["node-a"].Rgw, "omitted rgw must stay nil (untouched)")
}

// TestRgwPlacementDisabledRoundTrips confirms {enabled:false} round-trips
// through marshal/unmarshal as a non-nil remove intent (not dropped to nil).
func TestRgwPlacementDisabledRoundTrips(t *testing.T) {
	rgw := &RgwPlacement{Enabled: false, Port: 80}
	policy := PlacementPolicy{
		Mode: PlacementModeReconcile,
		Members: map[string]MemberPlacement{
			"node-a": {Rgw: rgw},
		},
	}
	data, err := json.Marshal(policy)
	require.NoError(t, err)

	var back PlacementPolicy
	require.NoError(t, json.Unmarshal(data, &back))
	require.NotNil(t, back.Members["node-a"].Rgw, "enabled:false must not be dropped to nil")
	assert.False(t, back.Members["node-a"].Rgw.Enabled)
	assert.Equal(t, 80, back.Members["node-a"].Rgw.Port)
}

// TestRgwObservedFrontendMarshal confirms the observed frontend reports ports
// and a TLS flag only, and omits empty ports via omitempty.
func TestRgwObservedFrontendMarshal(t *testing.T) {
	om := PlacementObservedMember{
		Member:      "node-a",
		Rgw:         true,
		RgwFrontend: &RgwObservedFrontend{Port: 80, SSLPort: 443, SSL: true},
	}
	data, err := json.Marshal(om)
	require.NoError(t, err)

	var back PlacementObservedMember
	require.NoError(t, json.Unmarshal(data, &back))
	assert.True(t, back.Rgw)
	require.NotNil(t, back.RgwFrontend)
	assert.Equal(t, 80, back.RgwFrontend.Port)
	assert.Equal(t, 443, back.RgwFrontend.SSLPort)
	assert.True(t, back.RgwFrontend.SSL)
}

// TestRgwObservedFrontendOmittedWhenNil confirms an absent frontend serializes
// without the field (omitempty) so non-RGW members do not carry a stub.
func TestRgwObservedFrontendOmittedWhenNil(t *testing.T) {
	om := PlacementObservedMember{Member: "node-a"}
	data, err := json.Marshal(om)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "rgw_frontend")
}
