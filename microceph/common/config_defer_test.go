package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBootstrapConfigDeferCephRoundTrip verifies that DeferCeph survives
// encode/decode round-trip (CE142).
func TestBootstrapConfigDeferCephRoundTrip(t *testing.T) {
	original := BootstrapConfig{
		MonIp:      "1.1.1.1",
		PublicNet:  "1.1.1.1/24",
		ClusterNet: "1.1.1.1/24",
		DeferCeph:  true,
	}

	encoded := EncodeBootstrapConfig(original)
	assert.Equal(t, "true", encoded["DeferCeph"])

	var decoded BootstrapConfig
	DecodeBootstrapConfig(encoded, &decoded)
	assert.Equal(t, original.DeferCeph, decoded.DeferCeph)
}

// TestBootstrapConfigDeferCephFalseRoundTrip verifies false survives round-trip.
func TestBootstrapConfigDeferCephFalseRoundTrip(t *testing.T) {
	original := BootstrapConfig{
		DeferCeph: false,
	}

	encoded := EncodeBootstrapConfig(original)
	assert.Equal(t, "false", encoded["DeferCeph"])

	var decoded BootstrapConfig
	DecodeBootstrapConfig(encoded, &decoded)
	assert.False(t, decoded.DeferCeph)
}

// TestJoinConfigDeferCephRoundTrip verifies DeferCeph in JoinConfig survives
// encode/decode round-trip (CE142).
func TestJoinConfigDeferCephRoundTrip(t *testing.T) {
	original := JoinConfig{
		AvailabilityZone: "az-0",
		DeferCeph:        true,
	}

	encoded := EncodeJoinConfig(original)
	assert.Equal(t, "true", encoded["DeferCeph"])

	var decoded JoinConfig
	DecodeJoinConfig(encoded, &decoded)
	assert.Equal(t, original.DeferCeph, decoded.DeferCeph)
	assert.Equal(t, original.AvailabilityZone, decoded.AvailabilityZone)
}
