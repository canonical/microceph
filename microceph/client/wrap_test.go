package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
)

// queryCall records what one fakeClient.Query invocation was given.
type queryCall struct {
	hasDeadline bool
	remaining   time.Duration
	ctxErr      error
	method      string
	prefix      mcTypes.EndpointPrefix
	url         string
	input       any
}

// fakeClient is a minimal mcTypes.Client. Only Query is functional: it records
// the context and request it receives, then returns the canned members or error.
type fakeClient struct {
	members    []mcTypes.ClusterMember
	err        error
	calls      []queryCall
	useTargets []string
}

func (f *fakeClient) Query(ctx context.Context, method string, prefix mcTypes.EndpointPrefix, path *url.URL, in any, out any) error {
	deadline, ok := ctx.Deadline()
	call := queryCall{hasDeadline: ok, ctxErr: ctx.Err(), method: method, prefix: prefix, url: path.String(), input: in}
	if ok {
		call.remaining = time.Until(deadline)
	}
	f.calls = append(f.calls, call)

	if f.err != nil {
		return f.err
	}

	members, ok := out.(*[]mcTypes.ClusterMember)
	if ok {
		*members = f.members
	}

	return nil
}

func (f *fakeClient) URL() *url.URL { panic("not implemented") }

func (f *fakeClient) HTTP() *http.Client { panic("not implemented") }

func (f *fakeClient) QueryRaw(context.Context, string, mcTypes.EndpointPrefix, *url.URL, any) (*http.Response, error) {
	panic("not implemented")
}

func (f *fakeClient) Websocket(context.Context, mcTypes.EndpointPrefix, *url.URL) (*websocket.Conn, error) {
	panic("not implemented")
}

func (f *fakeClient) SetClusterNotification() { panic("not implemented") }

func (f *fakeClient) UseTarget(target string) mcTypes.Client {
	f.useTargets = append(f.useTargets, target)
	return f
}

// assertBoundedCall checks that Query was called exactly once with a live
// context that already carries the clusterQueryTimeout deadline.
func assertBoundedCall(t *testing.T, f *fakeClient) queryCall {
	t.Helper()
	require.Len(t, f.calls, 1)
	call := f.calls[0]
	assert.True(t, call.hasDeadline, "Query must receive a context with a deadline")
	assert.NoError(t, call.ctxErr, "context must still be live while Query runs")
	assert.Greater(t, call.remaining, clusterQueryTimeout-5*time.Second)
	assert.LessOrEqual(t, call.remaining, clusterQueryTimeout)
	return call
}

func TestGetClusterMembersAttachesDeadline(t *testing.T) {
	f := &fakeClient{members: []mcTypes.ClusterMember{
		{ClusterMemberLocal: mcTypes.ClusterMemberLocal{Name: "node-a"}},
		{ClusterMemberLocal: mcTypes.ClusterMemberLocal{Name: "node-b"}},
	}}

	names, err := ClientImpl{}.GetClusterMembers(f)

	require.NoError(t, err)
	assert.Equal(t, []string{"node-a", "node-b"}, names)
	call := assertBoundedCall(t, f)
	assert.Equal(t, "GET", call.method)
	assert.Equal(t, mcTypes.PublicEndpoint, call.prefix)
	assert.Equal(t, "/cluster", call.url)
}

func TestGetClusterMembersEmpty(t *testing.T) {
	f := &fakeClient{}

	names, err := ClientImpl{}.GetClusterMembers(f)

	require.NoError(t, err)
	assert.Empty(t, names)
	assertBoundedCall(t, f)
}

func TestGetClusterMembersReturnsErrorUnchanged(t *testing.T) {
	for _, want := range []error{context.Canceled, context.DeadlineExceeded, errors.New("boom")} {
		f := &fakeClient{err: want}

		names, err := ClientImpl{}.GetClusterMembers(f)

		assert.Nil(t, names)
		// Same value, not merely errors.Is: the wrapper must not rewrap or retry.
		assert.Equal(t, want, err)
		assertBoundedCall(t, f)
	}
}

func TestDeleteClusterMemberAttachesDeadline(t *testing.T) {
	f := &fakeClient{}

	err := ClientImpl{}.DeleteClusterMember(f, "node-b", false)

	require.NoError(t, err)
	call := assertBoundedCall(t, f)
	assert.Equal(t, "DELETE", call.method)
	assert.Equal(t, mcTypes.PublicEndpoint, call.prefix)
	assert.Equal(t, "/cluster/node-b", call.url)
}

func TestDeleteClusterMemberForceAndError(t *testing.T) {
	want := errors.New("boom")
	f := &fakeClient{err: want}

	err := ClientImpl{}.DeleteClusterMember(f, "node-b", true)

	assert.Equal(t, want, err)
	call := assertBoundedCall(t, f)
	assert.Equal(t, "/cluster/node-b?force=1", call.url)
}

func TestDisableManagedSMBSerializesAndRoutesOnlyMemberRemoval(t *testing.T) {
	tests := []struct {
		name        string
		request     *types.ManagedSMBRemoval
		wantTargets []string
	}{
		{name: "member", request: &types.ManagedSMBRemoval{ClusterID: "files", Target: "node-a"}, wantTargets: []string{"node-a"}},
		{name: "deployment", request: &types.ManagedSMBRemoval{ClusterID: "files"}},
		{name: "logical cluster", request: &types.ManagedSMBRemoval{ClusterID: "files", Force: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeClient{}

			err := DisableManagedSMB(context.Background(), fake, test.request)

			require.NoError(t, err)
			require.Len(t, fake.calls, 1)
			assert.Equal(t, http.MethodDelete, fake.calls[0].method)
			assert.Equal(t, "/managed-services/smb", fake.calls[0].url)
			assert.Equal(t, test.request, fake.calls[0].input)
			assert.Equal(t, test.wantTargets, fake.useTargets)
			assert.True(t, fake.calls[0].hasDeadline)
			assert.Greater(t, fake.calls[0].remaining, managedSMBClientTimeout-5*time.Second)
		})
	}
}

func TestDisableManagedSMBRejectsInvalidRequestWithoutTransport(t *testing.T) {
	for _, request := range []*types.ManagedSMBRemoval{
		nil,
		{ClusterID: "files", Target: "node-a", Force: true},
		{ClusterID: "files", Target: "node/a"},
	} {
		fake := &fakeClient{}

		err := DisableManagedSMB(context.Background(), fake, request)

		require.Error(t, err)
		assert.Empty(t, fake.calls)
		assert.Empty(t, fake.useTargets)
	}
}
