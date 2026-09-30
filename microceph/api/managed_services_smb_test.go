package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

func TestManagedSMBPutAcceptsRequestsWithoutDatabaseAdmission(t *testing.T) {
	original := enableManagedSMBFunc
	t.Cleanup(func() { enableManagedSMBFunc = original })
	entered := make(chan context.Context, 2)
	enableManagedSMBFunc = func(ctx context.Context, _ interfaces.StateInterface, _ types.ManagedSMBService) error {
		entered <- ctx
		return nil
	}
	for range 2 {
		request := httptest.NewRequest(http.MethodPut, "/1.0/managed-services/smb", strings.NewReader(`{"cluster_id":"files","wait":false}`))
		recorder := httptest.NewRecorder()
		// No database is provided: managed admission must not reserve or lock a group.
		response := cmdManagedSMBPut(&mocks.MockState{}, request)
		require.NoError(t, response.Render(recorder, request))
		require.Equal(t, http.StatusOK, recorder.Code)
	}
	for range 2 {
		select {
		case ctx := <-entered:
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("worker did not finish")
			}
		case <-time.After(time.Second):
			t.Fatal("request was not dispatched")
		}
	}
}

func TestManagedSMBBackgroundOwnsDetachedBoundedContext(t *testing.T) {
	original := enableManagedSMBFunc
	t.Cleanup(func() { enableManagedSMBFunc = original })
	entered := make(chan context.Context, 1)
	proceed := make(chan struct{})
	enableManagedSMBFunc = func(ctx context.Context, _ interfaces.StateInterface, _ types.ManagedSMBService) error {
		entered <- ctx
		<-proceed
		return errors.New("worker failed")
	}
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodPut, "/1.0/managed-services/smb", strings.NewReader(`{"cluster_id":"files","wait":false}`)).WithContext(requestCtx)
	response := cmdManagedSMBPut(&mocks.MockState{}, request)
	recorder := httptest.NewRecorder()
	require.NoError(t, response.Render(recorder, request))
	require.Equal(t, http.StatusOK, recorder.Code)
	ctx := <-entered
	cancel()
	_, bounded := ctx.Deadline()
	require.True(t, bounded)
	require.NoError(t, ctx.Err(), "HTTP completion/disconnect must not cancel the worker")
	close(proceed)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("worker did not release its context after failure")
	}
}

func TestManagedSMBPutEnablesTargetThroughOrchestrator(t *testing.T) {
	originalEnable := enableManagedSMBFunc
	t.Cleanup(func() {
		enableManagedSMBFunc = originalEnable
	})
	var received types.ManagedSMBService
	var target string
	enableManagedSMBFunc = func(_ context.Context, state interfaces.StateInterface, request types.ManagedSMBService) error {
		received = request
		target = state.ClusterState().Name()
		return nil
	}
	body := `{
		"cluster_id":"files",
		"credentials":{"users":[{"name":"smbuser","password":"secret"}]},
		"bind_networks":["192.0.2.0/24"],
		"port":1445,
		"wait":true
	}`
	request := httptest.NewRequest(http.MethodPut, "/1.0/managed-services/smb", strings.NewReader(body))
	state := &mocks.MockState{ClusterName: "node-a"}
	recorder := httptest.NewRecorder()

	response := cmdManagedSMBPut(state, request)
	_ = response.Render(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "node-a", target)
	assert.Equal(t, types.ManagedSMBService{
		ClusterID:    "files",
		Credentials:  &types.SMBCredentials{Users: []types.SMBUser{{Name: "smbuser", Password: "secret"}}},
		BindNetworks: []string{"192.0.2.0/24"},
		Port:         1445,
		Wait:         true,
	}, received)
}

func TestManagedSMBPutRejectsInvalidSecretInputWithoutEcho(t *testing.T) {
	for _, body := range []string{
		`{"cluster_id":"files","secret-as-field":true}`,
		`{"cluster_id":"files","credentials":{"users":"secret-as-value"}}`,
		`{"cluster_id":"files"}` + strings.Repeat(" ", 2<<20) + `{"secret":true}`,
	} {
		request := httptest.NewRequest(http.MethodPut, "/1.0/managed-services/smb", strings.NewReader(body))
		recorder := httptest.NewRecorder()
		response := cmdManagedSMBPut(nil, request)
		require.NoError(t, response.Render(recorder, request))
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.NotContains(t, recorder.Body.String(), "secret")
	}
}

func TestValidateManagedSMBRequestRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		request types.ManagedSMBService
		message string
	}{
		{name: "cluster ID", request: types.ManagedSMBService{ClusterID: "?"}, message: "expected cluster_id to be valid"},
		{name: "two bind selectors", request: types.ManagedSMBService{ClusterID: "files", BindAddresses: []string{"192.0.2.10"}, BindNetworks: []string{"192.0.2.0/24"}}, message: "either addresses or networks"},
		{name: "bind address", request: types.ManagedSMBService{ClusterID: "files", BindAddresses: []string{"invalid"}}, message: `invalid SMB bind address "invalid"`},
		{name: "bind network", request: types.ManagedSMBService{ClusterID: "files", BindNetworks: []string{"invalid"}}, message: `invalid SMB bind network "invalid"`},
		{name: "negative port", request: types.ManagedSMBService{ClusterID: "files", Port: -1}, message: "SMB port must be 0 or between 1 and 65535"},
		{name: "large port", request: types.ManagedSMBService{ClusterID: "files", Port: 65536}, message: "SMB port must be 0 or between 1 and 65535"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateManagedSMBRequest(test.request)
			require.ErrorContains(t, err, test.message)
		})
	}

	require.NoError(t, validateManagedSMBRequest(types.ManagedSMBService{ClusterID: "files", Port: 0}))
}

func TestManagedSMBDeleteDisablesTargetThroughOrchestrator(t *testing.T) {
	originalDisable := disableManagedSMBFunc
	t.Cleanup(func() {
		disableManagedSMBFunc = originalDisable
	})
	var clusterID string
	var target string
	disableManagedSMBFunc = func(_ context.Context, state interfaces.StateInterface, value string) error {
		clusterID = value
		target = state.ClusterState().Name()
		return nil
	}
	body, err := json.Marshal(types.SMBService{ClusterID: "files"})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodDelete, "/1.0/managed-services/smb", strings.NewReader(string(body)))
	state := &mocks.MockState{ClusterName: "node-b"}
	recorder := httptest.NewRecorder()

	response := cmdManagedSMBDelete(state, request)
	_ = response.Render(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "files", clusterID)
	assert.Equal(t, "node-b", target)
}
