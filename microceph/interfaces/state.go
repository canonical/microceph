// Package common interfaces the microcluster cluster state
package interfaces

import (
	"github.com/canonical/microcluster/state"
)

// StateInterface for retrieving cluster state
type StateInterface interface {
	ClusterState() *state.State
}

// CephState holds cluster state
type CephState struct {
	State *state.State
}

// ClusterState gets the cluster state
func (c CephState) ClusterState() *state.State {
	return c.State
}
