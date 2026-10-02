package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/ceph"
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

func TestCmdAuthRotatePostSuccess(t *testing.T) {
	origExec := executeAuthRotationFunc
	defer func() { executeAuthRotationFunc = origExec }()

	// The handler returns the initialized record: the rotation itself runs
	// detached on the daemon and is tracked via auth status.
	executeAuthRotationFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string, clientName string) (*database.AuthRotationRecord, error) {
		assert.Equal(t, "aes256k", targetKeyType)
		assert.Equal(t, "client.rgw", clientName)
		return &database.AuthRotationRecord{
			TargetKeyType: "aes256k",
			State:         database.AuthRotationStateInProgress,
			Stage:         database.AuthRotationStageReadiness,
			ClientName:    "client.rgw",
		}, nil
	}

	body := strings.NewReader(`{"key_type": "aes256k", "client": "client.rgw"}`)
	req := httptest.NewRequest(http.MethodPost, "/1.0/auth/rotate", body)
	rec := httptest.NewRecorder()

	resp := cmdAuthRotatePost(nil, req)
	err := resp.Render(rec, req)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, rec.Code)

	var raw struct {
		Metadata types.AuthRotateResponse `json:"metadata"`
	}
	err = json.NewDecoder(rec.Body).Decode(&raw)
	require.NoError(t, err)

	assert.Equal(t, "aes256k", raw.Metadata.TargetKeyType)
	assert.Equal(t, "in_progress", raw.Metadata.State)
	assert.Equal(t, "client.rgw", raw.Metadata.ClientName)
}

func TestCmdAuthRotatePostError(t *testing.T) {
	origExec := executeAuthRotationFunc
	defer func() { executeAuthRotationFunc = origExec }()

	executeAuthRotationFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string, clientName string) (*database.AuthRotationRecord, error) {
		return nil, fmt.Errorf("readiness failed")
	}

	body := strings.NewReader(`{"key_type": "aes256k"}`)
	req := httptest.NewRequest(http.MethodPost, "/1.0/auth/rotate", body)
	rec := httptest.NewRecorder()

	resp := cmdAuthRotatePost(nil, req)
	err := resp.Render(rec, req)
	require.NoError(t, err)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestCmdAuthStatusGet(t *testing.T) {
	origBuild := buildAuthStatusFunc
	defer func() { buildAuthStatusFunc = origBuild }()

	buildAuthStatusFunc = func(ctx context.Context, s interfaces.StateInterface) (types.AuthStatusResponse, error) {
		return types.AuthStatusResponse{
			Status: "All client aes256k",
			State:  "completed",
			ClientDistribution: map[string][]string{
				"aes256k": {"client.admin", "client.rgw"},
			},
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/1.0/auth/status", nil)
	rec := httptest.NewRecorder()

	resp := cmdAuthStatusGet(nil, req)
	err := resp.Render(rec, req)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, rec.Code)

	var raw struct {
		Metadata types.AuthStatusResponse `json:"metadata"`
	}
	err = json.NewDecoder(rec.Body).Decode(&raw)
	require.NoError(t, err)

	assert.Equal(t, "All client aes256k", raw.Metadata.Status)
	assert.Equal(t, "completed", raw.Metadata.State)
	assert.Len(t, raw.Metadata.ClientDistribution["aes256k"], 2)
}

func TestCmdAuthRotateMemberPost(t *testing.T) {
	origMember := rotateMemberDaemonsFunc
	defer func() { rotateMemberDaemonsFunc = origMember }()

	// 1. Success: summary mapped onto the response.
	rotateMemberDaemonsFunc = func(ctx context.Context, s interfaces.StateInterface, keyType string, monKeyring string) (*ceph.MemberRotationSummary, error) {
		assert.Equal(t, "aes256k", keyType)
		assert.Equal(t, "[mon.]\n\tkey = MONKEY==\n", monKeyring)
		return &ceph.MemberRotationSummary{
			Hostname:     "node-a",
			MonRestarted: true,
			RotatedMgrs:  []string{"node-a"},
			RotatedOSDs:  []int64{0, 2},
		}, nil
	}

	body := strings.NewReader(`{"key_type": "aes256k", "mon_keyring": "[mon.]\n\tkey = MONKEY==\n"}`)
	req := httptest.NewRequest(http.MethodPost, "/1.0/auth/rotate/member", body)
	rec := httptest.NewRecorder()

	resp := cmdAuthRotateMemberPost(nil, req)
	err := resp.Render(rec, req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var raw struct {
		Metadata types.MemberAuthRotateResponse `json:"metadata"`
	}
	err = json.NewDecoder(rec.Body).Decode(&raw)
	require.NoError(t, err)
	assert.Equal(t, "node-a", raw.Metadata.Hostname)
	assert.True(t, raw.Metadata.MonRestarted)
	assert.Equal(t, []string{"node-a"}, raw.Metadata.RotatedMgrs)
	assert.Equal(t, []string{"0", "2"}, raw.Metadata.RotatedOSDs)

	// 2. Missing key_type is rejected.
	body = strings.NewReader(`{"mon_keyring": "[mon.]"}`)
	req = httptest.NewRequest(http.MethodPost, "/1.0/auth/rotate/member", body)
	rec = httptest.NewRecorder()
	resp = cmdAuthRotateMemberPost(nil, req)
	err = resp.Render(rec, req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// 3. Member rotation failure surfaces as an internal error.
	rotateMemberDaemonsFunc = func(ctx context.Context, s interfaces.StateInterface, keyType string, monKeyring string) (*ceph.MemberRotationSummary, error) {
		return nil, fmt.Errorf("mgr start failed")
	}
	body = strings.NewReader(`{"key_type": "aes256k"}`)
	req = httptest.NewRequest(http.MethodPost, "/1.0/auth/rotate/member", body)
	rec = httptest.NewRecorder()
	resp = cmdAuthRotateMemberPost(nil, req)
	err = resp.Render(rec, req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	// 4. Resume progress: a member named in the request's skip list replies
	//    without rotating anything again.
	rotateMemberDaemonsFunc = func(ctx context.Context, s interfaces.StateInterface, keyType string, monKeyring string) (*ceph.MemberRotationSummary, error) {
		t.Error("member handler must not rotate a member in the skip list")
		return nil, fmt.Errorf("must not be called")
	}
	body = strings.NewReader(`{"key_type": "aes256k", "skip": ["node-a"]}`)
	req = httptest.NewRequest(http.MethodPost, "/1.0/auth/rotate/member", body)
	rec = httptest.NewRecorder()
	resp = cmdAuthRotateMemberPost(&mocks.MockState{ClusterName: "node-a"}, req)
	err = resp.Render(rec, req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	raw.Metadata = types.MemberAuthRotateResponse{}
	err = json.NewDecoder(rec.Body).Decode(&raw)
	require.NoError(t, err)
	assert.Equal(t, "node-a", raw.Metadata.Hostname)
	assert.False(t, raw.Metadata.MonRestarted)
	assert.Empty(t, raw.Metadata.RotatedMgrs)
}

func TestCmdAuthRotatePostAbort(t *testing.T) {
	origExec := executeAuthRotationFunc
	origAbort := abortAuthRotationFunc
	defer func() {
		executeAuthRotationFunc = origExec
		abortAuthRotationFunc = origAbort
	}()

	executeAuthRotationFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string, clientName string) (*database.AuthRotationRecord, error) {
		t.Error("abort must not start a rotation")
		return nil, fmt.Errorf("must not be called")
	}

	// 1. Aborting an incomplete rotation: the response describes the record
	//    that was aborted.
	abortAuthRotationFunc = func(ctx context.Context, s interfaces.StateInterface) (*database.AuthRotationRecord, bool, error) {
		return &database.AuthRotationRecord{
			TargetKeyType: "aes256k",
			State:         database.AuthRotationStateBlocked,
			Stage:         database.AuthRotationStageRotateClients,
			ClientName:    "client.radosgw.gateway",
			Blocker:       "session incompatible",
		}, true, nil
	}

	body := strings.NewReader(`{"abort": true}`)
	req := httptest.NewRequest(http.MethodPost, "/1.0/auth/rotate", body)
	rec := httptest.NewRecorder()
	resp := cmdAuthRotatePost(nil, req)
	err := resp.Render(rec, req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var raw struct {
		Metadata types.AuthRotateResponse `json:"metadata"`
	}
	err = json.NewDecoder(rec.Body).Decode(&raw)
	require.NoError(t, err)
	assert.Equal(t, "blocked", raw.Metadata.State)
	assert.Equal(t, "aes256k", raw.Metadata.TargetKeyType)
	assert.Equal(t, "client.radosgw.gateway", raw.Metadata.ClientName)

	// 2. Nothing incomplete: refused.
	abortAuthRotationFunc = func(ctx context.Context, s interfaces.StateInterface) (*database.AuthRotationRecord, bool, error) {
		return &database.AuthRotationRecord{State: database.AuthRotationStateIdle}, false, nil
	}
	body = strings.NewReader(`{"abort": true}`)
	req = httptest.NewRequest(http.MethodPost, "/1.0/auth/rotate", body)
	rec = httptest.NewRecorder()
	resp = cmdAuthRotatePost(nil, req)
	err = resp.Render(rec, req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// 3. Abort combined with a filter is rejected.
	abortAuthRotationFunc = func(ctx context.Context, s interfaces.StateInterface) (*database.AuthRotationRecord, bool, error) {
		t.Error("abort with a filter must be rejected before touching the record")
		return nil, false, fmt.Errorf("must not be called")
	}
	body = strings.NewReader(`{"abort": true, "key_type": "aes256k"}`)
	req = httptest.NewRequest(http.MethodPost, "/1.0/auth/rotate", body)
	rec = httptest.NewRecorder()
	resp = cmdAuthRotatePost(nil, req)
	err = resp.Render(rec, req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
