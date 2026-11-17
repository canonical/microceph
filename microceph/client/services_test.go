package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/canonical/lxd/shared/api"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
)

// capabilityClient answers the capabilities query with a fixed body or error
// and records which target was addressed. The embedded interface is nil; only
// the two methods the check uses are implemented.
type capabilityClient struct {
	mcTypes.Client
	target   string
	response types.Capabilities
	err      error
}

func (c *capabilityClient) UseTarget(name string) mcTypes.Client {
	c.target = name
	return c
}

func (c *capabilityClient) Query(_ context.Context, _ string, _ mcTypes.EndpointPrefix, _ *url.URL, _ any, out any) error {
	if c.err != nil {
		return c.err
	}
	data, err := json.Marshal(c.response)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// TestCheckRGWPlacementSupport verifies the mixed-version gate: only a target
// advertising placement-rgw is accepted, an absent endpoint is refused as a
// bad request, and a transport failure stays an operational error.
func TestCheckRGWPlacementSupport(t *testing.T) {
	cases := []struct {
		name      string
		client    *capabilityClient
		wantErr   bool
		wantIs400 bool
	}{
		{"supported", &capabilityClient{response: types.Capabilities{Supported: []string{"declarative-placement", "placement-rgw"}}}, false, false},
		{"marker missing", &capabilityClient{response: types.Capabilities{Supported: []string{"declarative-placement"}}}, true, true},
		{"endpoint absent on old snap", &capabilityClient{err: api.StatusErrorf(http.StatusNotFound, "not found")}, true, true},
		{"transport failure", &capabilityClient{err: errors.New("connection refused")}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckRGWPlacementSupport(context.Background(), tc.client, "node-b")
			assert.Equal(t, "node-b", tc.client.target, "the target member itself must be queried")
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tc.wantIs400, api.StatusErrorCheck(err, http.StatusBadRequest))
		})
	}
}
