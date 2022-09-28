package main

import (
	"github.com/canonical/microcluster/state"
)

type hooks struct {
	bootstrap func(s *state.State) error
	start     func(s *state.State) error
	join      func(s *state.State) error
	remove    func(s *state.State) error
	heartbeat func(s *state.State) error
}

// OnBootstrapHook is run after the daemon is initialized and bootstrapped.
func (e hooks) OnBootstrapHook(s *state.State) error {
	if e.bootstrap == nil {
		return nil
	}

	return e.bootstrap(s)
}

// OnStartHook is run after the daemon is started.
func (e hooks) OnStartHook(s *state.State) error {
	if e.start == nil {
		return nil
	}

	return e.start(s)
}

// OnJoinHook is run after the daemon is initialized and joins a cluster.
func (e hooks) OnJoinHook(s *state.State) error {
	if e.join == nil {
		return nil
	}

	return e.join(s)
}

// OnRemoveHook is run after the daemon is removed from a cluster.
func (e hooks) OnRemoveHook(s *state.State) error {
	if e.remove == nil {
		return nil
	}

	return e.remove(s)
}

// OnHeartbeatHook is run after a successful heartbeat round.
func (e hooks) OnHeartbeatHook(s *state.State) error {
	if e.heartbeat == nil {
		return nil
	}

	return e.heartbeat(s)
}
