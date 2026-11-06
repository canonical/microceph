package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/canonical/lxd/lxd/response"
	mcrest "github.com/canonical/microcluster/rest"
	mcstate "github.com/canonical/microcluster/state"

	"github.com/canonical/microceph/microceph/clusterstate"
)

func TestServersKeepPublicAndUnixRoutes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &clusterstate.State{Context: ctx}
	called := false
	handler := func(got *clusterstate.State, r *http.Request) response.Response {
		called = true
		if got != s || got.Context.Err() != nil || r.Context().Err() == nil {
			t.Fatal("handler lost daemon state or request cancellation")
		}
		return response.EmptySyncResponse
	}
	action := EndpointAction{Handler: handler, ProxyTarget: true}
	servers := adaptServers(func(mcstate.State) *clusterstate.State { return s }, []Endpoint{{
		Path: "example", Get: action, Put: action, Post: action, Delete: action,
	}, {Path: "empty"}})
	server, ok := servers["microceph"]
	if !ok || !server.CoreAPI || !server.ServeUnix || server.PreInit {
		t.Fatal("application API must be available on core and Unix listeners only after initialization")
	}
	if len(server.Resources) != 1 || string(server.Resources[0].PathPrefix) != "1.0" {
		t.Fatal("application API must retain its /1.0 prefix")
	}
	routes := server.Resources[0].Endpoints
	if len(routes) != 2 || routes[0].Path != "example" || routes[1].Path != "empty" {
		t.Fatal("endpoint paths were not retained")
	}
	if routes[1].Get.Handler != nil {
		t.Fatal("absent actions must remain absent")
	}
	requestCtx, stopRequest := context.WithCancel(ctx)
	stopRequest()
	r := httptest.NewRequest("GET", "/1.0/example", nil).WithContext(requestCtx)
	for _, a := range []mcrest.EndpointAction{routes[0].Get, routes[0].Put, routes[0].Post, routes[0].Delete} {
		if !a.ProxyTarget || a.AllowUntrusted || a.AccessHandler != nil || a.Handler == nil {
			t.Fatal("action proxy and trust settings were not preserved")
		}
		called = false
		a.Handler(nil, r)
		if !called {
			t.Fatal("application handler was not invoked")
		}
	}
}
