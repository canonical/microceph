package mocks

import mcTypes "github.com/canonical/microcluster/v3/microcluster/types"

// MockStore is a minimal mock for mcTypes.Store that only implements
// RemotesByName. All other methods are nil via the embedded interface. It is
// intended for unit tests that exercise code paths reading the truststore.
type MockStore struct {
	mcTypes.Store

	// Remotes is the name->Remote map returned by RemotesByName.
	Remotes map[string]mcTypes.Remote
}

// RemotesByName returns a copy of the configured Remotes map, keyed by remote
// name. The hook under test only iterates the keys, so the returned map may be
// the test's own (values are not dereferenced); a defensive copy is returned
// anyway so a test cannot mutate the configured set through the result.
func (m *MockStore) RemotesByName() map[string]mcTypes.Remote {
	out := make(map[string]mcTypes.Remote, len(m.Remotes))
	for k, v := range m.Remotes {
		out[k] = v
	}
	return out
}
