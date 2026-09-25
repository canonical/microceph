package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
)

type fakeAuthClient struct {
	method string
	prefix mcTypes.EndpointPrefix
	url    string
	in     any
	out    any
	err    error
}

func (f *fakeAuthClient) Query(ctx context.Context, method string, prefix mcTypes.EndpointPrefix, path *url.URL, in any, out any) error {
	f.method = method
	f.prefix = prefix
	f.url = path.String()
	f.in = in

	if f.err != nil {
		return f.err
	}

	if f.out != nil && out != nil {
		switch o := out.(type) {
		case *types.AuthRotateResponse:
			if resp, ok := f.out.(*types.AuthRotateResponse); ok {
				*o = *resp
			}
		case *types.AuthStatusResponse:
			if resp, ok := f.out.(*types.AuthStatusResponse); ok {
				*o = *resp
			}
		}
	}

	return nil
}

func (f *fakeAuthClient) URL() *url.URL { return nil }

func (f *fakeAuthClient) HTTP() *http.Client { return nil }

func (f *fakeAuthClient) QueryRaw(context.Context, string, mcTypes.EndpointPrefix, *url.URL, any) (*http.Response, error) {
	return nil, nil
}

func (f *fakeAuthClient) Websocket(context.Context, mcTypes.EndpointPrefix, *url.URL) (*websocket.Conn, error) {
	return nil, nil
}

func (f *fakeAuthClient) SetClusterNotification() {}

func (f *fakeAuthClient) UseTarget(string) mcTypes.Client { return f }

func TestRotateAuthClient(t *testing.T) {
	c := &fakeAuthClient{
		out: &types.AuthRotateResponse{
			TargetKeyType: "aes256k",
			State:         "completed",
			Stage:         "finish_safely",
		},
	}

	resp, err := RotateAuth(context.Background(), c, "aes256k", "client.rgw")
	require.NoError(t, err)
	assert.Equal(t, "POST", c.method)
	assert.Equal(t, types.ExtendedPathPrefix, c.prefix)
	assert.Equal(t, "/auth/rotate", c.url)

	req, ok := c.in.(types.AuthRotateRequest)
	require.True(t, ok)
	assert.Equal(t, "aes256k", req.KeyType)
	assert.Equal(t, "client.rgw", req.Client)

	assert.Equal(t, "aes256k", resp.TargetKeyType)
	assert.Equal(t, "completed", resp.State)
}

func TestRotateAuthClientError(t *testing.T) {
	c := &fakeAuthClient{
		err: fmt.Errorf("connection refused"),
	}

	_, err := RotateAuth(context.Background(), c, "aes256k", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to rotate auth keys")
}

func TestGetAuthStatusClient(t *testing.T) {
	c := &fakeAuthClient{
		out: &types.AuthStatusResponse{
			Status: "All client aes256k",
			State:  "completed",
		},
	}

	resp, err := GetAuthStatus(context.Background(), c)
	require.NoError(t, err)
	assert.Equal(t, "GET", c.method)
	assert.Equal(t, types.ExtendedPathPrefix, c.prefix)
	assert.Equal(t, "/auth/status", c.url)
	assert.Equal(t, "All client aes256k", resp.Status)
	assert.Equal(t, "completed", resp.State)
}
