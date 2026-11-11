package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	mcclient "github.com/canonical/microcluster/v2/client"
	"github.com/canonical/microcluster/v2/microcluster"

	"github.com/canonical/microceph/microceph/api/types"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip dispatches a captured request without network I/O.
func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestApplicationClientPublicPrefix(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		call   func(context.Context, *mcclient.Client) error
	}{
		{"disks", "GET", "/1.0/disks", func(ctx context.Context, c *mcclient.Client) error { _, err := GetDisks(ctx, c); return err }},
		{"services", "GET", "/1.0/services", func(ctx context.Context, c *mcclient.Client) error { _, err := GetServices(ctx, c); return err }},
		{"configs", "PUT", "/1.0/configs", func(ctx context.Context, c *mcclient.Client) error { return SetConfig(ctx, c, &types.Config{}) }},
		{"client configs", "PUT", "/1.0/client/configs/test", func(ctx context.Context, c *mcclient.Client) error {
			return SetClientConfig(ctx, c, &types.ClientConfig{Key: "test"})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, err := microcluster.App(microcluster.Args{StateDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			c, err := app.LocalClient()
			if err != nil {
				t.Fatal(err)
			}
			type contextKey struct{}
			ctx := context.WithValue(context.Background(), contextKey{}, "caller")
			called := false
			c.Client.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("request route changed: %s %s", r.Method, r.URL.Path)
				}
				_, deadline := r.Context().Deadline()
				if !deadline || r.Context().Value(contextKey{}) != "caller" {
					t.Error("client lost its timeout or caller context")
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"type":"sync","status":"Success","status_code":200,"metadata":null}`)),
					Request:    r,
				}, nil
			})}
			err = tc.call(ctx, c)
			if err != nil || !called {
				t.Fatalf("request did not complete: called=%v, error=%v", called, err)
			}
		})
	}
}
