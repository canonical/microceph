// Package rest describes MicroCeph routes independently of native Microcluster state.
package rest

import (
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	mcrest "github.com/canonical/microcluster/rest"
	mcstate "github.com/canonical/microcluster/state"

	"github.com/canonical/microceph/microceph/api/endpoint"
	"github.com/canonical/microceph/microceph/clusterstate"
)

// EndpointAction describes a protected application action and optional target forwarding.
type EndpointAction struct {
	// Handler implements the application action.
	Handler func(*clusterstate.State, *http.Request) response.Response
	// ProxyTarget permits forwarding to the member named by ?target=.
	ProxyTarget bool
}

// Endpoint describes the route and methods used by the MicroCeph application API.
type Endpoint struct {
	// Path is the route relative to the public API prefix.
	Path string
	// Get handles GET requests.
	Get EndpointAction
	// Put handles PUT requests.
	Put EndpointAction
	// Post handles POST requests.
	Post EndpointAction
	// Delete handles DELETE requests.
	Delete EndpointAction
}

// Servers binds application routes to the core API and Unix control socket.
func Servers(bridge *clusterstate.Bridge, endpoints []Endpoint) map[string]mcrest.Server {
	return adaptServers(bridge.State, endpoints)
}

func adaptServers(wrap func(mcstate.State) *clusterstate.State, endpoints []Endpoint) map[string]mcrest.Server {
	action := func(a EndpointAction) mcrest.EndpointAction {
		out := mcrest.EndpointAction{ProxyTarget: a.ProxyTarget}
		if a.Handler != nil {
			out.Handler = func(s mcstate.State, r *http.Request) response.Response {
				return a.Handler(wrap(s), r)
			}
		}
		return out
	}

	routes := make([]mcrest.Endpoint, 0, len(endpoints))
	for _, e := range endpoints {
		routes = append(routes, mcrest.Endpoint{
			Path: e.Path, Get: action(e.Get), Put: action(e.Put),
			Post: action(e.Post), Delete: action(e.Delete),
		})
	}
	return map[string]mcrest.Server{
		"microceph": {
			CoreAPI:   true,
			ServeUnix: true,
			Resources: []mcrest.Resources{{PathPrefix: endpoint.PublicEndpoint, Endpoints: routes}},
		},
	}
}
