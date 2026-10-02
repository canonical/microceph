package client

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
)

func TestGetClusterTokenDeadline(t *testing.T) {
	fake := &fakeClient{}

	_, err := GetClusterToken(context.Background(), fake, types.ClusterExportRequest{RemoteName: "siteb"})
	require.NoError(t, err)
	require.Len(t, fake.calls, 1)

	call := fake.calls[0]
	assert.Equal(t, "GET", call.method)
	assert.True(t, call.hasDeadline)
	// The export runs two ceph auth commands on the daemon; a 5 s deadline was
	// too short on loaded hosts.
	assert.Greater(t, call.remaining, 60*time.Second)
	assert.LessOrEqual(t, call.remaining, clusterExportTimeout)
}
