// Package clusterstate isolates MicroCeph's application state from Microcluster APIs.
package clusterstate

import (
	"context"
	"database/sql"
	"net/http"
	"sync"

	"github.com/canonical/lxd/lxd/cluster/request"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microcluster/client"
	mcstate "github.com/canonical/microcluster/state"
)

// Database is the database contract used by MicroCeph's application helpers.
type Database interface {
	IsOpen() bool
	Transaction(context.Context, func(context.Context, *sql.Tx) error) error
}

// State carries the member bindings and lifecycle context used by MicroCeph.
// Keeping this boundary application-owned also permits small database-free test fixtures.
type State struct {
	// Context follows the daemon lifecycle, including for asynchronous work.
	Context context.Context
	// Database supplies application transactions and readiness checks.
	Database Database
	// Address resolves this member's API address.
	Address func() *api.URL
	// Name resolves this member's name.
	Name func() string
	// Leader returns a client connected to the database leader.
	Leader func() (*client.Client, error)
	// Cluster returns notification clients for the other members.
	Cluster func(*http.Request) (client.Cluster, error)
	// RemoteAddresses resolves member names to their trusted cluster IPs.
	RemoteAddresses func() map[string]string
}

// Bridge binds application handlers to the daemon's lifecycle, not a request's lifetime.
type Bridge struct {
	mu  sync.RWMutex
	ctx context.Context
}

// NewBridge creates a bridge with a startup context until the daemon supplies its own.
func NewBridge(ctx context.Context) *Bridge {
	return &Bridge{ctx: ctx}
}

// SetContext installs the daemon context supplied to its OnStart hook.
func (b *Bridge) SetContext(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ctx = ctx
}

// Context returns the context governing daemon-owned application work.
func (b *Bridge) Context() context.Context {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.ctx
}

// State adapts native member state without exposing Microcluster's internal types.
func (b *Bridge) State(s mcstate.State) *State {
	ctx := b.Context()
	app := newState(ctx, s.Address, s.Name, adaptDatabase(ctx, s.Database()), s.Leader, notificationCluster(s.Cluster))
	app.RemoteAddresses = func() map[string]string {
		addresses := make(map[string]string)
		for name, remote := range s.Remotes().RemotesByName() {
			addresses[name] = remote.Address.Addr().String()
		}
		return addresses
	}
	return app
}

func newState(ctx context.Context, address func() *api.URL, name func() string, db Database, leader func() (*client.Client, error), cluster func(*http.Request) (client.Cluster, error)) *State {
	return &State{Context: ctx, Database: db, Address: address, Name: name, Leader: leader, Cluster: cluster}
}

func notificationCluster(get func(bool) (client.Cluster, error)) func(*http.Request) (client.Cluster, error) {
	return func(r *http.Request) (client.Cluster, error) {
		if r != nil {
			r.Header.Set("User-Agent", request.UserAgentNotifier)
		}
		// Quincy's previous implementation always made notification clients.
		return get(true)
	}
}
