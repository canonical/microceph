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
	"github.com/canonical/microceph/microceph/database"
	"github.com/canonical/microceph/microceph/interfaces"
)

func TestCmdAuthRotatePostSuccess(t *testing.T) {
	origExec := executeAuthRotationFunc
	defer func() { executeAuthRotationFunc = origExec }()

	executeAuthRotationFunc = func(ctx context.Context, s interfaces.StateInterface, targetKeyType string, clientName string) (*database.AuthRotationRecord, error) {
		assert.Equal(t, "aes256k", targetKeyType)
		assert.Equal(t, "client.rgw", clientName)
		return &database.AuthRotationRecord{
			TargetKeyType: "aes256k",
			State:         database.AuthRotationStateCompleted,
			Stage:         database.AuthRotationStageFinishSafely,
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
	assert.Equal(t, "completed", raw.Metadata.State)
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
