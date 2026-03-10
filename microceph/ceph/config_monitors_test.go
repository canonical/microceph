package ceph

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetMonitorsFromConfigUsesPrefixMatch(t *testing.T) {
	config := map[string]string{
		"mon.host.node1":     "10.0.0.1",
		"foo.mon.host.node2": "10.0.0.2",
		"mon.hostextra":      "10.0.0.3",
	}

	addresses := getMonitorsFromConfig(config)
	assert.ElementsMatch(t, []string{"10.0.0.1"}, addresses)
}

func TestGetMonitorsFromConfigForMembersFiltersStale(t *testing.T) {
	config := map[string]string{
		"mon.host.node1": "10.0.0.1",
		"mon.host.node2": "10.0.0.2",
		"mon.host.1":     "10.0.0.10",
	}

	addresses := getMonitorsFromConfigForMembers(config, []string{"node1"})
	assert.ElementsMatch(t, []string{"10.0.0.1", "10.0.0.10"}, addresses)
}

func TestStaleMonHostKeys(t *testing.T) {
	config := map[string]string{
		"mon.host.node1": "10.0.0.1",
		"mon.host.node2": "10.0.0.2",
		"mon.host.2":     "10.0.0.20",
		"public_network": "10.0.0.0/24",
	}

	keys := staleMonHostKeys(config, []string{"node1"})
	assert.Equal(t, []string{"mon.host.node2"}, keys)
}
