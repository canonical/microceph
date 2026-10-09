package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
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
	// Other tests launch workers which may still be logging after cancellation.
	// Isolate capture of the process-global logger rather than race those workers.
	const childEnv = "MICROCEPH_TEST_SMB_DECODE_LOG"
	if os.Getenv(childEnv) != "1" {
		executable, err := os.Executable()
		require.NoError(t, err)
		command := exec.Command(executable, "-test.run=^TestManagedSMBPutRejectsInvalidSecretInputWithoutEcho$")
		command.Env = append(os.Environ(), childEnv+"=1")
		output, err := command.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}

	logFile, err := os.Create(filepath.Join(t.TempDir(), "daemon.log"))
	require.NoError(t, err)
	originalLogger := logger.DaemonLogger
	t.Cleanup(func() {
		logger.DaemonLogger = originalLogger
		_ = logFile.Close()
	})
	// NewLogger captures stdout when its handler is constructed.
	originalStdout := os.Stdout
	os.Stdout = logFile
	capturedLogger, err := logger.NewLogger("")
	os.Stdout = originalStdout
	require.NoError(t, err)
	logger.DaemonLogger = capturedLogger

	for _, body := range []string{
		`{"cluster_id":"files","secret-as-field":true}`,
		`{"cluster_id":"files","credentials":{"users":"secret-as-value"}}`,
		`{"cluster_id":"files","credentials":{"users":[{"name":"alice","password":"secret"}]}`, // Truncated JSON.
		`{"cluster_id":"files"} {"secret":true}`,
		`{"cluster_id":"files"}` + strings.Repeat(" ", 2<<20) + `{"secret":true}`,
	} {
		before, err := logFile.Stat()
		require.NoError(t, err)
		request := httptest.NewRequest(http.MethodPut, "/1.0/managed-services/smb", strings.NewReader(body))
		recorder := httptest.NewRecorder()
		response := cmdManagedSMBPut(nil, request)
		require.NoError(t, response.Render(recorder, request))
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.NotContains(t, recorder.Body.String(), "secret")
		logs, err := os.ReadFile(logFile.Name())
		require.NoError(t, err)
		entry := string(logs[before.Size():])
		require.Contains(t, entry, "level=ERROR")
		require.Contains(t, entry, `msg="failed decoding managed SMB enable request: invalid JSON"`)
		require.NotContains(t, entry, "secret")
		require.NotContains(t, entry, "alice")
		require.NotContains(t, entry, "unknown field")
		require.NotContains(t, entry, "unexpected EOF")
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

func TestManagedSMBDeleteDispatchesRequestedScope(t *testing.T) {
	originalDisable := disableManagedSMBFunc
	originalRemoveService := removeManagedSMBServiceFunc
	originalDeleteCluster := deleteManagedSMBClusterFunc
	t.Cleanup(func() {
		disableManagedSMBFunc = originalDisable
		removeManagedSMBServiceFunc = originalRemoveService
		deleteManagedSMBClusterFunc = originalDeleteCluster
	})
	called := ""
	disableManagedSMBFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) error {
		called = "member"
		return nil
	}
	removeManagedSMBServiceFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) error {
		called = "service"
		return nil
	}
	deleteManagedSMBClusterFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) error {
		called = "cluster"
		return nil
	}
	tests := []struct {
		name     string
		path     string
		body     string
		state    *mocks.MockState
		expected string
	}{
		{
			name:     "member",
			path:     "/1.0/managed-services/smb?target=node-b",
			body:     `{"cluster_id":"files","target":"node-b"}`,
			state:    &mocks.MockState{ClusterName: "node-b"},
			expected: "member",
		},
		{
			name:     "member case variant",
			path:     "/1.0/managed-services/smb?target=node-b",
			body:     `{"cluster_id":"files","TaRgEt":"node-b"}`,
			state:    &mocks.MockState{ClusterName: "node-b"},
			expected: "member",
		},
		{
			name:     "service",
			path:     "/1.0/managed-services/smb",
			body:     `{"cluster_id":"files"}`,
			state:    &mocks.MockState{},
			expected: "service",
		},
		{
			name:     "logical cluster",
			path:     "/1.0/managed-services/smb",
			body:     `{"cluster_id":"files","force":true}`,
			state:    &mocks.MockState{},
			expected: "cluster",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called = ""
			request := httptest.NewRequest(http.MethodDelete, test.path, strings.NewReader(test.body))
			recorder := httptest.NewRecorder()
			response := cmdManagedSMBDelete(test.state, request)
			require.NoError(t, response.Render(recorder, request))

			require.Equal(t, http.StatusOK, recorder.Code)
			assert.Equal(t, test.expected, called)
		})
	}
}

func TestManagedSMBDeleteRejectsBodyTargetThatDiffersFromReceivingMember(t *testing.T) {
	originalDisable := disableManagedSMBFunc
	t.Cleanup(func() {
		disableManagedSMBFunc = originalDisable
	})
	called := false
	disableManagedSMBFunc = func(_ context.Context, _ interfaces.StateInterface, _ string) error {
		called = true
		return nil
	}

	request := httptest.NewRequest(http.MethodDelete, "/1.0/managed-services/smb?target=node-a", strings.NewReader(`{"cluster_id":"files","target":"node-a"}`))
	recorder := httptest.NewRecorder()
	response := cmdManagedSMBDelete(&mocks.MockState{ClusterName: "node-b"}, request)
	require.NoError(t, response.Render(recorder, request))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.False(t, called)
}

func TestManagedSMBDeleteRejectsMalformedAndConflictingSelectorsBeforeDispatch(t *testing.T) {
	originalDisable := disableManagedSMBFunc
	originalRemoveService := removeManagedSMBServiceFunc
	originalDeleteCluster := deleteManagedSMBClusterFunc
	t.Cleanup(func() {
		disableManagedSMBFunc = originalDisable
		removeManagedSMBServiceFunc = originalRemoveService
		deleteManagedSMBClusterFunc = originalDeleteCluster
	})
	called := false
	notCalled := func(_ context.Context, _ interfaces.StateInterface, _ string) error {
		called = true
		return nil
	}
	disableManagedSMBFunc = notCalled
	removeManagedSMBServiceFunc = notCalled
	deleteManagedSMBClusterFunc = notCalled
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "unknown body field", path: "/1.0/managed-services/smb", body: `{"cluster_id":"files","secret":"not-logged"}`},
		{name: "explicit empty body target", path: "/1.0/managed-services/smb", body: `{"cluster_id":"files","target":""}`},
		{name: "null body target", path: "/1.0/managed-services/smb", body: `{"cluster_id":"files","target":null}`},
		{name: "case variant empty body target", path: "/1.0/managed-services/smb", body: `{"cluster_id":"files","Target":""}`},
		{name: "case variant null body target", path: "/1.0/managed-services/smb", body: `{"cluster_id":"files","TARGET":null}`},
		{name: "case variant empty body target with force", path: "/1.0/managed-services/smb", body: `{"cluster_id":"files","Target":"","force":true}`},
		{name: "case variant null body target with force", path: "/1.0/managed-services/smb", body: `{"cluster_id":"files","TARGET":null,"force":true}`},
		{name: "trailing body", path: "/1.0/managed-services/smb", body: `{"cluster_id":"files"} {}`},
		{name: "target and force", path: "/1.0/managed-services/smb?target=node-a", body: `{"cluster_id":"files","target":"node-a","force":true}`},
		{name: "global removal routed to member", path: "/1.0/managed-services/smb?target=node-a", body: `{"cluster_id":"files"}`},
		{name: "explicit empty routing target", path: "/1.0/managed-services/smb?target=", body: `{"cluster_id":"files"}`},
		{name: "malformed routing target", path: "/1.0/managed-services/smb?target=node-a;bad", body: `{"cluster_id":"files"}`},
		{name: "malformed routing target with force", path: "/1.0/managed-services/smb?target=node-a;bad", body: `{"cluster_id":"files","force":true}`},
		{name: "invalid routing target escape", path: "/1.0/managed-services/smb?target=%ZZ", body: `{"cluster_id":"files"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called = false
			request := httptest.NewRequest(http.MethodDelete, test.path, strings.NewReader(test.body))
			recorder := httptest.NewRecorder()
			response := cmdManagedSMBDelete(&mocks.MockState{ClusterName: "node-a"}, request)
			require.NoError(t, response.Render(recorder, request))

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			assert.False(t, called)
			assert.NotContains(t, recorder.Body.String(), "not-logged")
		})
	}
}
