package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microceph/microceph/api/types"
	"github.com/canonical/microceph/microceph/interfaces"
	"github.com/canonical/microceph/microceph/mocks"
)

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
		"define_user_pass":["smbuser%secret"],
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
		ClusterID:      "files",
		DefineUserPass: []string{"smbuser%secret"},
		BindNetworks:   []string{"192.0.2.0/24"},
		Port:           1445,
		Wait:           true,
	}, received)
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
