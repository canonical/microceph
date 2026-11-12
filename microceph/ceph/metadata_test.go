package ceph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestMdsID checks that only hostnames beginning with a digit are prefixed,
// since Ceph rejects MDS names that start with a numeric digit.
func TestMdsID(t *testing.T) {
	cases := []struct {
		hostname string
		want     string
	}{
		{hostname: "node-a", want: "node-a"},
		{hostname: "06-site-ceph-st-02", want: "mds-06-site-ceph-st-02"},
		{hostname: "1node", want: "mds-1node"},
		{hostname: "node1", want: "node1"},
		{hostname: "", want: ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, mdsID(c.hostname), "hostname %q", c.hostname)
		// Mapping an already mapped ID must not prefix it again.
		assert.Equal(t, c.want, mdsID(mdsID(c.hostname)), "hostname %q", c.hostname)
	}
}

// TestServiceDaemonID checks that only the MDS ID is remapped.
func TestServiceDaemonID(t *testing.T) {
	assert.Equal(t, "mds-1node", serviceDaemonID("mds", "1node"))
	assert.Equal(t, "1node", serviceDaemonID("mon", "1node"))
	assert.Equal(t, "1node", serviceDaemonID("mgr", "1node"))
	assert.Equal(t, "node-a", serviceDaemonID("mds", "node-a"))
}

// TestBootstrapMdsDigitHostname checks that the MDS keyring is created for
// the prefixed entity on a host whose name begins with a digit.
func TestBootstrapMdsDigitHostname(t *testing.T) {
	r := withMockRunner(t)
	r.On("RunCommand", "ceph", "auth", "get-or-create", "mds.mds-1node",
		"mon", "allow profile mds",
		"mgr", "allow profile mds",
		"mds", "allow *",
		"osd", "allow *",
		"-o", "/data/mds/ceph-mds-1node/keyring").
		Return("", nil).Once()

	err := bootstrapMds("1node", "/data/mds/ceph-mds-1node")
	assert.NoError(t, err)
}

// TestEvictMdsDigitHostname checks that `ceph mds fail` targets the prefixed
// MDS ID rather than the raw hostname.
func TestEvictMdsDigitHostname(t *testing.T) {
	r := withMockRunner(t)
	r.On("RunCommandContext", mock.Anything, "ceph", "mds", "fail", "mds-1node", "--yes-i-really-mean-it").
		Return("", nil).Once()

	err := evictMds(context.Background(), "1node")
	assert.NoError(t, err)
}
