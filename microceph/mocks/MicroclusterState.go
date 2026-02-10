package mocks

import (
	"net/url"

	"github.com/canonical/lxd/shared"
	state "github.com/canonical/microcluster/v3/state"
)

// MockState mocks the internal microcluster state.
type MockState struct {
	state.State

	URL         *url.URL
	ClusterName string
}

// Name returns the name supplied to MockState.
func (m *MockState) Name() string {
	return m.ClusterName
}

// Address returns the address supplied to MockState.
func (m *MockState) Address() *url.URL {
	return m.URL
}

// ServerCert is set to always return nil to prematurely return before making database actions.
func (m *MockState) ServerCert() *shared.CertInfo {
	return nil
}
