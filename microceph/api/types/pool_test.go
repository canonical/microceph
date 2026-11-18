package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPoolPutWantsDefault checks that a request without set_default, as sent by older clients,
// still sets the default pool size, and that an explicit value is honoured.
func TestPoolPutWantsDefault(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "field absent", body: `{"pools":["mypool"],"size":2}`, want: true},
		{name: "explicit true", body: `{"pools":["mypool"],"size":2,"set_default":true}`, want: true},
		{name: "explicit false", body: `{"pools":["mypool"],"size":2,"set_default":false}`, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var req PoolPut
			err := json.Unmarshal([]byte(tc.body), &req)
			require.NoError(t, err)

			assert.Equal(t, tc.want, req.WantsDefault())
		})
	}
}

// TestPoolPutEncodesExplicitFalse checks that a client asking not to set the default sends the
// field, since leaving it out would make the daemon set the default.
func TestPoolPutEncodesExplicitFalse(t *testing.T) {
	setDefault := false
	out, err := json.Marshal(PoolPut{Pools: []string{"mypool"}, Size: 2, SetDefault: &setDefault})
	require.NoError(t, err)

	assert.Contains(t, string(out), `"set_default":false`)
}
