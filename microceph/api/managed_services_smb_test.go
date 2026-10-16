package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/logger"
	"github.com/canonical/microceph/microceph/mocks"
)

func stubManagedSMBAdmission(t *testing.T) {
	t.Helper()
	acquire, release := acquireManagedSMBOperationFunc, releaseManagedSMBOperationFunc
	t.Cleanup(func() {
		acquireManagedSMBOperationFunc, releaseManagedSMBOperationFunc = acquire, release
	})
	acquireManagedSMBOperationFunc = func(_ context.Context, _ interfaces.StateInterface, _, _ string, _ int64) (bool, error) {
		return true, nil
	}
	releaseManagedSMBOperationFunc = func(_ context.Context, _ interfaces.StateInterface, _, _ string) (bool, error) {
		return true, nil
	}
}

func TestManagedSMBRequestsRejectBusyBeforeBackgroundDispatch(t *testing.T) {
	stubManagedSMBAdmission(t)
	original := enableManagedSMBFunc
	t.Cleanup(func() { enableManagedSMBFunc = original })
	var mu sync.Mutex
	held := ""
	acquireManagedSMBOperationFunc = func(_ context.Context, _ interfaces.StateInterface, clusterID, token string, expiresAt int64) (bool, error) {
		require.Equal(t, "files", clusterID)
		require.NotEmpty(t, token)
		require.InDelta(t, 20*60, expiresAt-time.Now().Unix(), 2)
		mu.Lock()
		defer mu.Unlock()
		if held != "" {
			return false, nil
		}
		held = token
		return true, nil
	}
	releaseManagedSMBOperationFunc = func(_ context.Context, _ interfaces.StateInterface, _, token string) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		if held != token {
			return false, nil
		}
		held = ""
		return true, nil
	}
	entered := make(chan context.Context, 2)
	proceed := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(proceed) })
	defer unblock()
	enableManagedSMBFunc = func(ctx context.Context, _ interfaces.StateInterface, _ types.ManagedSMBService) error {
		entered <- ctx
		<-proceed
		return nil
	}
	request := httptest.NewRequest(http.MethodPut, "/1.0/managed-services/smb", strings.NewReader(`{"cluster_id":"files","wait":false}`))
	recorder := httptest.NewRecorder()
	response := cmdManagedSMBPut(&mocks.MockState{ClusterName: "node-a"}, request)
	require.NoError(t, response.Render(recorder, request))
	require.Equal(t, http.StatusOK, recorder.Code)
	ctx := <-entered
	t.Cleanup(func() { unblock(); <-ctx.Done() })
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		request = httptest.NewRequest(method, "/1.0/managed-services/smb", strings.NewReader(`{"cluster_id":"files"}`))
		recorder = httptest.NewRecorder()
		state := &mocks.MockState{ClusterName: "node-b"}
		if method == http.MethodPut {
			response = cmdManagedSMBPut(state, request)
		} else {
			response = cmdManagedSMBDelete(state, request)
		}
		require.NoError(t, response.Render(recorder, request))
		require.Equal(t, http.StatusConflict, recorder.Code)
		require.Contains(t, recorder.Body.String(), "operation in progress")
	}
	unblock()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
	request = httptest.NewRequest(http.MethodPut, "/1.0/managed-services/smb", strings.NewReader(`{"cluster_id":"files","wait":true}`))
	recorder = httptest.NewRecorder()
	response = cmdManagedSMBPut(&mocks.MockState{ClusterName: "node-b"}, request)
	require.NoError(t, response.Render(recorder, request))
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestManagedSMBOperationReleaseAndUncertainOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		workErr    error
		released   bool
		releaseErr error
		wantCalls  int
		wantCode   int
	}{
		{"success", nil, true, nil, 1, http.StatusOK},
		{"known failure", errors.New("validation failed"), true, nil, 1, http.StatusInternalServerError},
		{"uncertain", ceph.ErrManagedSMBOutcomeUnknown, true, nil, 0, http.StatusInternalServerError},
		{"timeout", context.DeadlineExceeded, true, nil, 0, http.StatusInternalServerError},
		{"ownership lost", nil, false, nil, 1, http.StatusInternalServerError},
		{"release failed", nil, false, errors.New("database unavailable"), 1, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubManagedSMBAdmission(t)
			calls := 0
			releaseManagedSMBOperationFunc = func(ctx context.Context, _ interfaces.StateInterface, _, _ string) (bool, error) {
				calls++
				require.NoError(t, ctx.Err())
				_, bounded := ctx.Deadline()
				require.True(t, bounded)
				return tc.released, tc.releaseErr
			}
			request := httptest.NewRequest(http.MethodPut, "/1.0/managed-services/smb", nil)
			response := runManagedSMBRequest(request.Context(), interfaces.CephState{State: &mocks.MockState{}}, "files", true, func(context.Context) error { return tc.workErr })
			recorder := httptest.NewRecorder()
			require.NoError(t, response.Render(recorder, request))
			require.Equal(t, tc.wantCode, recorder.Code)
			require.Equal(t, tc.wantCalls, calls)
		})
	}
}

func TestManagedSMBAdmissionFailureDoesNotDispatch(t *testing.T) {
	stubManagedSMBAdmission(t)
	acquireManagedSMBOperationFunc = func(context.Context, interfaces.StateInterface, string, string, int64) (bool, error) {
		return false, errors.New("database unavailable")
	}
	request := httptest.NewRequest(http.MethodPut, "/1.0/managed-services/smb", nil)
	response := runManagedSMBRequest(request.Context(), interfaces.CephState{State: &mocks.MockState{}}, "files", false, func(context.Context) error {
		t.Error("work dispatched after failed admission")
		return nil
	})
	recorder := httptest.NewRecorder()
	require.NoError(t, response.Render(recorder, request))
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
}

func TestManagedSMBFinalizationDoesNotAdmitAnotherRequestBeforeCompletion(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`
CREATE TABLE service_groups (id INTEGER PRIMARY KEY, service TEXT, group_id TEXT, config TEXT, UNIQUE(service, group_id));
CREATE TABLE grouped_services (id INTEGER PRIMARY KEY, service_group_id INTEGER);`)
	require.NoError(t, err)
	state := interfaces.CephState{State: &mocks.MockState{
		Cert: &shared.CertInfo{},
		DBObj: &mocks.MockDB{TxFn: func(ctx context.Context, work func(context.Context, *sql.Tx) error) error {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback() }()
			err = work(ctx, tx)
			if err != nil {
				return err
			}
			return tx.Commit()
		}},
	}}
	request := httptest.NewRequest(http.MethodDelete, "/1.0/managed-services/smb", nil)
	response := runManagedSMBRequest(request.Context(), state, "files", true, func(ctx context.Context) error {
		// Simulate the native finalization callback while its managed caller is
		// still waiting. No DB transaction is held over this work.
		err := database.FinalizeSMBServiceGroup(ctx, state, "files")
		require.NoError(t, err)
		contender := runManagedSMBRequest(ctx, state, "files", false, func(context.Context) error {
			t.Error("finalization released admission before the outer request ended")
			return nil
		})
		recorder := httptest.NewRecorder()
		require.NoError(t, contender.Render(recorder, request))
		require.Equal(t, http.StatusConflict, recorder.Code)
		return nil
	})
	recorder := httptest.NewRecorder()
	require.NoError(t, response.Render(recorder, request))
	require.Equal(t, http.StatusOK, recorder.Code)
	var groups int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM service_groups`).Scan(&groups))
	require.Zero(t, groups, "release removes the finalized metadata-only row")
}

func TestManagedSMBBackgroundOwnsDetachedBoundedContext(t *testing.T) {
	stubManagedSMBAdmission(t)
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
	stubManagedSMBAdmission(t)
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
	stubManagedSMBAdmission(t)
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
