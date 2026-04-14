// Package common interfaces the microcluster cluster state
package interfaces

import (
	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
)

// StateInterface for retrieving cluster state
type StateInterface interface {
	ClusterState() mcTypes.State
}

// CephState holds cluster state
type CephState struct {
	State mcTypes.State
}

// ClusterState gets the cluster state
func (c CephState) ClusterState() mcTypes.State {
	return c.State
}
