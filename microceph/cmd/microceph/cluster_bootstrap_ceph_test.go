package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestClusterBootstrapCephTargetRequired verifies that --target is required;
// running without it returns an error.
func TestClusterBootstrapCephTargetRequired(t *testing.T) {
	cmd := &cmdClusterBootstrapCeph{
		common:  &CmdControl{FlagStateDir: "/tmp/nonexistent"},
		cluster: &cmdCluster{},
	}

	c := cmd.Command()
	c.SetArgs([]string{})
	err := c.RunE(c, []string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--target is required")
}

// TestClusterBootstrapCephFlags verifies that CLI flags are parsed correctly,
// including the --availability-zone flag (N7).
func TestClusterBootstrapCephFlags(t *testing.T) {
	cmd := &cmdClusterBootstrapCeph{
		common:  &CmdControl{FlagStateDir: "/tmp/nonexistent"},
		cluster: &cmdCluster{},
	}

	c := cmd.Command()
	err := c.ParseFlags([]string{
		"--target", "node-a",
		"--mon-ip", "10.0.0.1",
		"--public-network", "10.0.0.0/24",
		"--availability-zone", "az-0",
	})
	assert.NoError(t, err)
	assert.Equal(t, "node-a", cmd.flagTarget)
	assert.Equal(t, "10.0.0.1", cmd.flagMonIp)
	assert.Equal(t, "10.0.0.0/24", cmd.flagPubNet)
	assert.Equal(t, "az-0", cmd.flagAvailabilityZone)
}

// TestClusterBootstrapCephBuildsRequestWithTargetAndAZ (B4/N7) verifies that
// the CLI builds a CephBootstrapRequest carrying both the target member and the
// availability zone. This tests the request-building path without requiring a
// live microcluster daemon.
func TestClusterBootstrapCephBuildsRequestWithTargetAndAZ(t *testing.T) {
	cmd := &cmdClusterBootstrapCeph{
		common:               &CmdControl{FlagStateDir: "/tmp/nonexistent"},
		cluster:              &cmdCluster{},
		flagTarget:           "node-b",
		flagMonIp:            "10.0.0.1",
		flagPubNet:           "10.0.0.0/24",
		flagClusterNet:       "10.0.0.0/24",
		flagV2Only:           true,
		flagAvailabilityZone: "az-1",
	}

	req := cmd.buildRequest()

	assert.Equal(t, "node-b", req.Target)
	assert.Equal(t, "10.0.0.1", req.MonIp)
	assert.Equal(t, "10.0.0.0/24", req.PublicNet)
	assert.Equal(t, "10.0.0.0/24", req.ClusterNet)
	assert.True(t, req.V2Only)
	assert.Equal(t, "az-1", req.AvailabilityZone)
	assert.False(t, req.Force)
}

// TestClusterBootstrapCephForceFlag verifies that --force is parsed and
// threaded into the request (FIX 1b).
func TestClusterBootstrapCephForceFlag(t *testing.T) {
	cmd := &cmdClusterBootstrapCeph{
		common:     &CmdControl{FlagStateDir: "/tmp/nonexistent"},
		cluster:    &cmdCluster{},
		flagTarget: "node-b",
		flagForce:  true,
	}

	req := cmd.buildRequest()
	assert.True(t, req.Force, "--force must be threaded into the request")
}
