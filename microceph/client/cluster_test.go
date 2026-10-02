package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	microCli "github.com/canonical/microcluster/v2/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
)

const exportToken = "c2l0ZWItdG9rZW4="

// deadlineTransport replies to cluster export requests and records the request deadline.
type deadlineTransport struct {
	hasDeadline bool
	remaining   time.Duration
}

func (rt *deadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	deadline, ok := r.Context().Deadline()
	rt.hasDeadline = ok
	if rt.hasDeadline {
		rt.remaining = time.Until(deadline)
	}

	reply := `{"type": "sync", "status": "Success", "status_code": 200, "metadata": "` + exportToken + `"}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(reply)),
		Request:    r,
	}, nil
}

func TestGetClusterTokenDeadline(t *testing.T) {
	transport := &deadlineTransport{}

	// microcluster has no public constructor for its client, so use a zero value
	// and replace the HTTP client it sends requests with.
	cli := &microCli.Client{}
	cli.Client.Client = &http.Client{Transport: transport}

	token, err := GetClusterToken(context.Background(), cli, types.ClusterExportRequest{RemoteName: "siteb"})
	require.NoError(t, err)
	assert.Equal(t, exportToken, token)
	assert.True(t, transport.hasDeadline)
	// The export reads the cluster config and runs two ceph auth commands on the
	// daemon; a 5 s deadline was too short on loaded hosts. microcluster adds a
	// 30 s deadline to a request that has none, which this must not fall back to.
	assert.Greater(t, transport.remaining, 60*time.Second)
	assert.LessOrEqual(t, transport.remaining, clusterExportTimeout)
}
