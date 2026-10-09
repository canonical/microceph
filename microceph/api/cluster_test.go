package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/common"
	"github.com/canonical/microceph/microceph/mocks"
)

// The request context must reach the ceph command, and its key must appear in
// the exported config instead of an unbounded second command's output.
func TestClusterGetExportsRemoteKeyBoundToRequest(t *testing.T) {
	original := common.ProcessExec
	t.Cleanup(func() { common.ProcessExec = original })
	r := mocks.NewRunner(t)
	common.ProcessExec = r

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const key = "AQDK+/9nN3kSJRAAv0JpZ2xk6oVqRk3m9eHcPg=="
	r.On("RunCommandContext", mock.Anything, "ceph", "auth", "get-or-create", "client.siteb",
		"mon", "allow *", "osd", "allow *", "mds", "allow *", "mgr", "allow *", "--format", "json").
		Run(func(args mock.Arguments) {
			cephCtx := args.Get(0).(context.Context)
			assert.NoError(t, cephCtx.Err())
			cancel()
			assert.ErrorIs(t, cephCtx.Err(), context.Canceled)
		}).
		// The ceph CLI prints a blank line before a JSON reply.
		Return("\n"+`[{"entity":"client.siteb","key":"`+key+`"}]`+"\n", nil).Once()

	req := httptest.NewRequest(http.MethodGet, "/1.0/cluster", strings.NewReader(`{"remote_name":"siteb"}`)).WithContext(ctx)
	w := httptest.NewRecorder()
	resp := cmdClusterGet(&mocks.MockState{DBObj: &mocks.MockDB{}}, req)
	require.NoError(t, resp.Render(w, req))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var export struct {
		Metadata string `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &export))
	data, err := base64.StdEncoding.DecodeString(export.Metadata)
	require.NoError(t, err)

	var configs map[string]string
	require.NoError(t, json.Unmarshal(data, &configs))
	assert.Equal(t, key, configs["keyring.client.siteb"])
}
