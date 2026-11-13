package clusterstate

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microcluster/v2/client"
)

func TestBridgeUsesDaemonLifecycle(t *testing.T) {
	startup, stopStartup := context.WithCancel(context.Background())
	defer stopStartup()
	bridge := NewBridge(startup)
	if bridge.Context() != startup {
		t.Fatal("startup context was not retained")
	}

	daemon, stopDaemon := context.WithCancel(context.Background())
	defer stopDaemon()
	bridge.SetContext(daemon)
	stopStartup()
	if bridge.Context() != daemon || bridge.Context().Err() != nil {
		t.Fatal("bridge did not switch to the daemon lifecycle context")
	}

	requestContext, stopRequest := context.WithCancel(daemon)
	stopRequest()
	if requestContext.Err() == nil || bridge.Context().Err() != nil {
		t.Fatal("request completion must not cancel asynchronous daemon work")
	}
	stopDaemon()
	if !errors.Is(bridge.Context().Err(), context.Canceled) {
		t.Fatal("daemon shutdown must cancel work using the bridge")
	}
}

func TestApplicationStateKeepsBindings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	address := api.NewURL().Host("127.0.0.1:7443")
	s := newState(ctx, func() *api.URL { return address }, func() string { return "member" }, nil, nil, nil)
	if s.Context != ctx || s.Address() != address || s.Name() != "member" || s.Database != nil {
		t.Fatal("application state did not retain its context and member bindings")
	}
}

func TestNotificationClusterPreservesLegacyBehaviour(t *testing.T) {
	for _, withRequest := range []bool{false, true} {
		t.Run(map[bool]string{false: "nil request", true: "request"}[withRequest], func(t *testing.T) {
			wantErr := errors.New("cluster unavailable")
			called := false
			getClients := notificationCluster(func(notify bool) (client.Cluster, error) {
				called = true
				if !notify {
					t.Fatal("legacy cluster broadcasts must retain notification clients")
				}
				return nil, wantErr
			})
			var r *http.Request
			if withRequest {
				r = &http.Request{Header: make(http.Header)}
			}
			_, err := getClients(r)
			if !called || !errors.Is(err, wantErr) {
				t.Fatal("cluster result was not propagated")
			}
			if r != nil && r.Header.Get("User-Agent") != request.UserAgentNotifier {
				t.Fatal("request notification header was not preserved")
			}
		})
	}
}
