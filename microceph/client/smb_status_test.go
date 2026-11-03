package client

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
)

type smbStatusClient struct {
	mcTypes.Client
	target string
	query  func(context.Context, string, *url.URL, any) error
}

func (c *smbStatusClient) UseTarget(target string) mcTypes.Client { c.target = target; return c }
func (c *smbStatusClient) Query(ctx context.Context, method string, _ mcTypes.EndpointPrefix, path *url.URL, _ any, out any) error {
	return c.query(ctx, method, path, out)
}

func TestSMBStatusClientConsumesADAndBoundsDeadline(t *testing.T) {
	c := &smbStatusClient{}
	c.query = func(ctx context.Context, method string, path *url.URL, out any) error {
		require.Equal(t, "GET", method)
		require.Equal(t, "/services/smb", path.Path)
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 10*time.Second)
		return json.Unmarshal([]byte(`{"cluster_id":"alpha","auth_mode":"active-directory","realm":"EXAMPLE.TEST","ready":true,"stop_requested":false}`), out)
	}
	status, err := GetSMBService(context.Background(), c, "gateway-a")
	require.NoError(t, err)
	require.Equal(t, "gateway-a", c.target)
	require.Equal(t, types.SMBStatus{ClusterID: "alpha", AuthMode: "active-directory", Realm: "EXAMPLE.TEST", Ready: true}, status)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.query = func(ctx context.Context, _ string, _ *url.URL, _ any) error { return ctx.Err() }
	_, err = GetSMBService(ctx, c, "gateway-a")
	require.ErrorIs(t, err, context.Canceled)
}
